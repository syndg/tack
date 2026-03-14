package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
	"github.com/syndg/deck/internal/services/agents"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// MergeEnqueuer creates merge-queue entries for streams ready to merge.
type MergeEnqueuer interface {
	EnqueueStream(ctx context.Context, streamID string) error
}

// PlanCreator creates a plan from planner agent output.
type PlanCreator interface {
	CreatePlan(ctx context.Context, objectiveID string, agentOutput string) (*domain.Plan, error)
}

// MailSender sends mail messages (used for escalations via the broker).
type MailSender interface {
	Send(ctx context.Context, msg *domain.MailMessage) error
}

// Coordinator orchestrates objective execution from plan approval to completion.
// It subscribes to events and drives blueprint execution.
type Coordinator struct {
	engine         *blueprint.Engine
	scheduler      *Scheduler
	spawner        *Spawner
	lifecycle      *lifecycle.Manager
	mergeEnqueuer  MergeEnqueuer
	planCreator    PlanCreator
	mailSender     MailSender
	executions     *db.ExecutionStore
	objectives     *db.ObjectiveStore
	plans          *db.PlanStore
	streams        *db.StreamStore
	eventBus       *events.PersistentBus
	activityLogger *agents.ActivityLogger
	timeouts       config.TimeoutConfig
	logger         *slog.Logger

	ctx         context.Context // set in Start(); used as parent for execution goroutines
	mu          sync.Mutex
	activeExecs map[string]context.CancelFunc // objectiveID → cancel
	agentMap    map[string]*SpawnResult       // sessionID → spawn result
	terminated  map[string]bool               // sessionID → explicitly killed
	idleTimers  map[string]*time.Timer        // sessionID → idle timeout timer
}

// NewCoordinator creates a new Coordinator and registers step handlers with the engine.
func NewCoordinator(
	engine *blueprint.Engine,
	scheduler *Scheduler,
	spawner *Spawner,
	lc *lifecycle.Manager,
	mergeEnqueuer MergeEnqueuer,
	planCreator PlanCreator,
	mailSender MailSender,
	executions *db.ExecutionStore,
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	eventBus *events.PersistentBus,
	activityLogger *agents.ActivityLogger,
	timeouts config.TimeoutConfig,
	logger *slog.Logger,
) *Coordinator {
	c := &Coordinator{
		engine:         engine,
		scheduler:      scheduler,
		spawner:        spawner,
		lifecycle:      lc,
		mergeEnqueuer:  mergeEnqueuer,
		planCreator:    planCreator,
		mailSender:     mailSender,
		executions:     executions,
		objectives:     objectives,
		plans:          plans,
		streams:        streams,
		eventBus:       eventBus,
		activityLogger: activityLogger,
		timeouts:       timeouts,
		logger:         logger,
		activeExecs:    make(map[string]context.CancelFunc),
		agentMap:       make(map[string]*SpawnResult),
		terminated:     make(map[string]bool),
		idleTimers:     make(map[string]*time.Timer),
	}

	return c
}

// Start subscribes to events and begins processing.
// Subscribes to objective lifecycle events only; stream execution is coordinated
// from within the blueprint_ref handler to avoid double-claiming streams.
func (c *Coordinator) Start(ctx context.Context) error {
	c.ctx = ctx
	sub, unsub := c.eventBus.Subscribe(128)

	go func() {
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub:
				if !ok {
					return
				}
				switch event.Type {
				case domain.EventObjectiveCreated:
					c.handleObjectiveCreated(ctx, event)
				case domain.EventMergeCompleted:
					// After a merge completes, check if a partial objective
					// can now be upgraded to completed (retry path).
					if event.Objective != "" {
						c.checkPartialToCompleted(ctx, event.Objective)
					}
				}
			}
		}
	}()

	go c.recoverExecutions(ctx)

	c.logger.Info("coordinator started")
	return nil
}

// recoverExecutions resumes in-flight executions after a daemon restart.
// Only top-level executions (ParentID == "") are considered.
func (c *Coordinator) recoverExecutions(ctx context.Context) {
	allExecs, err := c.executions.List(ctx)
	if err != nil {
		c.logger.Error("execution recovery: failed to list executions", "error", err)
		return
	}

	for i := range allExecs {
		exec := &allExecs[i]

		// Only recover top-level executions.
		if exec.ParentID != "" {
			continue
		}

		switch exec.Status {
		case "running":
			obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
			if err != nil {
				c.logger.Error("execution recovery: failed to get objective",
					"execution_id", exec.ID,
					"objective_id", exec.ObjectiveID,
					"error", err,
				)
				continue
			}

			if obj.Status != domain.ObjectiveStatusExecuting {
				c.logger.Info("execution recovery: skipping, objective not in executing state",
					"execution_id", exec.ID,
					"objective_id", exec.ObjectiveID,
					"objective_status", string(obj.Status),
				)
				continue
			}

			execCtx, cancel := context.WithCancel(ctx)

			c.mu.Lock()
			if existing, ok := c.activeExecs[exec.ObjectiveID]; ok {
				existing()
			}
			c.activeExecs[exec.ObjectiveID] = cancel
			c.mu.Unlock()

			c.logger.Info("execution recovery: resuming running execution",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			go c.runExecution(execCtx, exec)

		case "waiting_human":
			c.logger.Info("execution recovery: execution awaiting human approval",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
		}
	}
}

// Stop cancels all active executions and cleans up.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for objectiveID, cancel := range c.activeExecs {
		c.logger.Info("cancelling active execution on stop", "objective_id", objectiveID)
		cancel()
	}
	c.activeExecs = make(map[string]context.CancelFunc)
	c.logger.Info("coordinator stopped")
}

// ResumeExecution re-launches the execution loop for an execution that was waiting
// for human approval. The execution must already have been approved via engine.ApproveHuman.
func (c *Coordinator) ResumeExecution(exec *blueprint.Execution) {
	baseCtx := c.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	execCtx, cancel := context.WithCancel(baseCtx)

	c.mu.Lock()
	if existing, ok := c.activeExecs[exec.ObjectiveID]; ok {
		existing()
	}
	c.activeExecs[exec.ObjectiveID] = cancel
	c.mu.Unlock()

	c.logger.Info("resuming execution after human approval",
		"execution_id", exec.ID,
		"objective_id", exec.ObjectiveID,
	)
	go c.runExecution(execCtx, exec)
}

// handleObjectiveCreated processes EventObjectiveCreated events.
// Every objective starts its blueprint execution immediately — the blueprint
// is the source of truth. If it has a plan step, the planner agent runs.
// If it has an approve step, execution pauses for human review.
func (c *Coordinator) handleObjectiveCreated(ctx context.Context, event domain.Event) {
	if event.Objective == "" {
		return
	}

	go func() {
		if err := c.StartExecution(ctx, event.Objective); err != nil {
			c.logger.Error("failed to start execution for new objective",
				"objective_id", event.Objective,
				"error", err,
			)
		}
	}()
}

// StartExecution begins blueprint execution for an objective.
// The blueprint is the source of truth — all steps run in order, including
// planner agents and human approval gates. No steps are skipped.
//
//  1. Fetch objective, verify it's in a startable state (planning or approved)
//  2. Determine blueprint name (from objective.Blueprint, default to "feature")
//  3. Create execution via engine.Start(blueprintName, objectiveID)
//  4. Transition objective to "executing" via lifecycle
//  5. Run the execution loop in a goroutine
func (c *Coordinator) StartExecution(ctx context.Context, objectiveID string) error {
	// 1. Fetch objective and verify it's startable.
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective %s: %w", objectiveID, err)
	}
	if obj.Status != domain.ObjectiveStatusPlanning && obj.Status != domain.ObjectiveStatusApproved {
		return fmt.Errorf("objective %s cannot start execution (status: %s)", objectiveID, obj.Status)
	}

	// 2. Determine blueprint name, defaulting to the shipped feature blueprint.
	blueprintName := obj.Blueprint
	if blueprintName == "" {
		blueprintName = "Feature Implementation"
	}

	// 3. Create execution via engine.Start — all blueprint steps run in order.
	exec, err := c.engine.Start(ctx, blueprintName, objectiveID)
	if err != nil {
		return fmt.Errorf("starting blueprint execution for objective %s: %w", objectiveID, err)
	}
	if err := c.executions.Create(ctx, exec); err != nil {
		return fmt.Errorf("persisting execution for objective %s: %w", objectiveID, err)
	}

	// 4. Transition objective to executing.
	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusExecuting); err != nil {
		return fmt.Errorf("transitioning objective %s to executing: %w", objectiveID, err)
	}

	// If a plan already exists (e.g. simple mode), also transition it.
	if plan, err := c.plans.GetByObjective(ctx, objectiveID); err == nil {
		_ = c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusExecuting)
	}

	// Publish EventExecutionStarted.
	evtPayload, _ := json.Marshal(map[string]string{
		"execution_id":   exec.ID,
		"blueprint_name": blueprintName,
	})
	c.eventBus.Publish(domain.Event{
		Type:      domain.EventExecutionStarted,
		Objective: objectiveID,
		Payload:   string(evtPayload),
		CreatedAt: time.Now(),
	})

	// 6. Run the execution loop in a goroutine.
	// Use the coordinator's long-lived context (not the caller's ctx) so that
	// executions survive HTTP request context cancellation.
	baseCtx := c.ctx
	if baseCtx == nil {
		baseCtx = ctx
	}
	execCtx, cancel := context.WithCancel(baseCtx)
	c.mu.Lock()
	c.activeExecs[objectiveID] = cancel
	c.mu.Unlock()

	go c.runExecution(execCtx, exec)

	c.logger.Info("execution started",
		"execution_id", exec.ID,
		"objective_id", objectiveID,
		"blueprint", blueprintName,
	)
	return nil
}

// runExecution drives the blueprint execution loop.
// Calls engine.Advance() repeatedly until the execution reaches a terminal state.
func (c *Coordinator) runExecution(ctx context.Context, exec *blueprint.Execution) {
	defer func() {
		c.mu.Lock()
		delete(c.activeExecs, exec.ObjectiveID)
		c.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			c.logger.Info("execution cancelled",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		}

		updated, err := c.engine.Advance(ctx, exec)
		if err != nil {
			if ctx.Err() != nil {
				c.logger.Info("execution advance cancelled",
					"execution_id", exec.ID,
					"objective_id", exec.ObjectiveID,
				)
				return
			}
			c.logger.Error("engine advance failed",
				"execution_id", exec.ID,
				"error", err,
			)
			c.failExecution(ctx, exec.ObjectiveID, fmt.Sprintf("engine advance failed: %v", err))
			return
		}
		exec = updated

		// Persist state after each advance.
		if err := c.executions.Update(ctx, exec); err != nil {
			c.logger.Error("failed to persist execution state",
				"execution_id", exec.ID,
				"error", err,
			)
		}

		switch exec.Status {
		case "completed":
			c.completeExecution(ctx, exec.ObjectiveID)
			c.logger.Info("execution completed",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		case "failed":
			c.failExecution(ctx, exec.ObjectiveID, "execution failed")
			c.logger.Error("execution failed",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		case "waiting_human":
			// Execution paused; resumes when engine.ApproveHuman is called.
			c.logger.Info("execution waiting for human approval",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		}
	}
}

// HandleAgentStep implements the StepHandler for agent-type blueprint steps.
// Registered with the engine as the StepTypeAgent handler.
// For single-stream executions (e.g. Hotfix) the lone stream is attached to the
// agent session so stream state and quality gates operate on the same sandbox.
func (c *Coordinator) HandleAgentStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	role := step.Role
	if role == "" {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q has no role defined", step.ID),
		}, nil
	}

	obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	// In a sub-execution, the stream is identified by exec.StreamID.
	// For top-level single-stream executions (e.g. hotfix), fall back to
	// singleStreamForObjective.
	var stream *domain.Stream
	if exec.StreamID != "" {
		stream, err = c.streams.Get(ctx, exec.StreamID)
		if err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("getting stream %s: %s", exec.StreamID, err),
			}, nil
		}
	} else {
		stream, err = c.singleStreamForObjective(ctx, exec.ObjectiveID)
		if err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("getting stream context: %s", err),
			}, nil
		}
	}
	if stream != nil && stream.Status == "pending" {
		if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("marking stream %s executing: %s", stream.ID, err),
			}, nil
		}
	}

	taskSpec := step.Description
	if taskSpec == "" && stream != nil {
		taskSpec = stream.Description
	}

	// Check for fix-loop context: if this step has fix_context metadata, the
	// engine routed us here via on_fail. Find the previous sandbox to reuse,
	// scoped to this specific stream and role so we don't grab a sandbox from
	// a different agent (e.g. scout or reviewer) within the same objective.
	var fixContext, reuseSandboxID string
	if state := exec.StepStates[step.ID]; state != nil && state.Metadata != nil {
		fixContext = state.Metadata["fix_context"]
	}
	if fixContext != "" {
		streamID := ""
		if stream != nil {
			streamID = stream.ID
		}
		if sb, err := c.spawner.FindSandboxForStep(ctx, exec.ObjectiveID, streamID, role); err == nil && sb != nil {
			reuseSandboxID = sb.ID()
		}
	}

	// Spawn the agent.
	result, err := c.spawner.Spawn(ctx, SpawnRequest{
		Objective:      obj,
		Stream:         stream,
		Role:           role,
		TaskSpec:       taskSpec,
		ExecutionID:    exec.ID,
		CommitMode:     string(step.EffectiveCommitMode()),
		Messages:       step.Messages,
		FixContext:     fixContext,
		ReuseSandboxID: reuseSandboxID,
	})
	if err != nil {
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("spawning agent for step %q: %s", step.ID, err),
		}, nil
	}

	// Track the spawn result.
	c.mu.Lock()
	c.agentMap[result.Session.ID] = result
	delete(c.terminated, result.Session.ID)
	c.mu.Unlock()

	c.logger.Info("agent spawned for step",
		"session_id", result.Session.ID,
		"role", role,
		"step", step.ID,
		"execution_id", exec.ID,
	)

	// Drain agent activity events to logger + event bus.
	c.drainAgentActivity(result.Session, result.Process)

	// Block until agent completes.
	agentResult, waitErr := result.Process.Wait()

	wasKilled := c.finishTrackedAgent(result.Session.ID)
	if wasKilled {
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		c.spawner.MarkFailed(ctx, result.Session, "killed")
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q failed: killed", step.ID),
		}, nil
	}

	if waitErr != nil || !agentResult.Success {
		errMsg := agentResult.Error
		if waitErr != nil {
			errMsg = waitErr.Error()
		}
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		c.spawner.MarkFailed(ctx, result.Session, errMsg)
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q failed: %s", step.ID, errMsg),
		}, nil
	}

	cleanSummary, generatedMessages := c.extractGeneratedMessages(step, agentResult.Summary)

	// Handle commit mode after successful agent completion.
	commitMode := step.EffectiveCommitMode()
	switch commitMode {
	case blueprint.CommitModeAuto:
		if err := c.autoCommit(ctx, result.Sandbox, obj.Description, generatedMessages.CommitMessage); err != nil {
			c.logger.Error("auto-commit failed", "step", step.ID, "error", err)
			// Non-fatal: changes are still in the worktree for the merge processor.
		}
	case blueprint.CommitModeAgent:
		// Verify the agent actually committed; fall back to auto if not.
		statusResult, err := result.Sandbox.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
		if err == nil && strings.TrimSpace(statusResult.Stdout) != "" {
			c.logger.Warn("agent mode set but uncommitted changes found, falling back to auto-commit", "step", step.ID)
			if err := c.autoCommit(ctx, result.Sandbox, obj.Description, generatedMessages.CommitMessage); err != nil {
				c.logger.Error("fallback auto-commit failed", "step", step.ID, "error", err)
			}
		}
	case blueprint.CommitModeNone:
		// No commit needed.
	}

	// If this was a planner agent, create the plan from its output.
	if role == string(domain.AgentRolePlanner) && c.planCreator != nil {
		plan, err := c.planCreator.CreatePlan(ctx, exec.ObjectiveID, cleanSummary)
		if err != nil {
			c.spawner.MarkFailed(ctx, result.Session, fmt.Sprintf("plan creation failed: %s", err))
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("creating plan from planner output: %s", err),
			}, nil
		}
		c.logger.Info("plan created from planner agent",
			"plan_id", plan.ID,
			"objective_id", exec.ObjectiveID,
		)
	}

	c.spawner.MarkCompleted(ctx, result.Session, cleanSummary)
	return blueprint.StepResult{
		Status:   blueprint.StepStatusCompleted,
		Output:   cleanSummary,
		Metadata: generatedMessages.ToMetadata(),
	}, nil
}

// streamResult reports the outcome of a stream's sub-execution.
type streamResult struct {
	StreamID string
	Error    string // empty on success
}

// HandleBlueprintRefStep implements the StepHandler for blueprint_ref steps.
// Creates a sub-execution of the referenced blueprint per stream and advances
// them concurrently. Handles dependency cascade, failure escalation, and partial
// completion.
func (c *Coordinator) HandleBlueprintRefStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	// 1. Resolve the referenced blueprint.
	refBP := c.resolveBlueprint(step.Ref)
	if refBP == nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("referenced blueprint %q not found", step.Ref),
		}, nil
	}

	// 2. Get the plan and all its streams.
	plan, err := c.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	allStreams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing streams for plan %s: %s", plan.ID, err),
		}, nil
	}

	if len(allStreams) == 0 {
		c.logger.Info("no streams for plan, completing blueprint_ref step immediately",
			"plan_id", plan.ID,
			"execution_id", exec.ID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	totalStreams := len(allStreams)

	// Build stream ID set and pre-count completed.
	streamSet := make(map[string]bool, totalStreams)
	completedCount := 0
	for _, s := range allStreams {
		streamSet[s.ID] = true
		if s.Status == "completed" {
			completedCount++
		}
	}
	if completedCount == totalStreams {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	// Determine failure policy.
	onStreamFailure := step.OnStreamFailure
	if onStreamFailure == "" {
		onStreamFailure = "escalate"
	}

	// 3. Subscribe to stream-ready events for cascade.
	sub, unsub := c.eventBus.Subscribe(128)
	defer unsub()

	// Results channel collects sub-execution outcomes.
	results := make(chan streamResult, totalStreams)

	// 4. Start sub-executions for initially ready streams.
	ready, err := c.scheduler.GetReadyStreams(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting ready streams: %s", err),
		}, nil
	}

	for i := range ready {
		stream := ready[i]
		if err := c.startStreamSubExecution(ctx, exec, refBP, &stream, plan.ID, results); err != nil {
			c.logger.Error("failed to start sub-execution for stream",
				"stream_id", stream.ID,
				"error", err,
			)
			results <- streamResult{StreamID: stream.ID, Error: fmt.Sprintf("failed to start: %v", err)}
		}
	}

	// 5. Collect results and handle cascade.
	resolvedCount := completedCount
	var failures []string

	for resolvedCount < totalStreams {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  "execution context cancelled",
			}, nil

		case res := <-results:
			resolvedCount++
			if res.Error != "" {
				failures = append(failures, fmt.Sprintf("stream %s: %s", res.StreamID, res.Error))

				if onStreamFailure == "escalate" {
					c.escalateStreamFailure(ctx, exec, step, res.StreamID, res.Error)
				}

				c.logger.Warn("stream sub-execution failed",
					"stream_id", res.StreamID,
					"error", res.Error,
					"resolved", resolvedCount,
					"total", totalStreams,
				)
			} else {
				c.logger.Info("stream sub-execution completed",
					"stream_id", res.StreamID,
					"resolved", resolvedCount,
					"total", totalStreams,
				)
			}

		case event, ok := <-sub:
			if !ok {
				continue
			}
			if event.Type != domain.EventStreamReady {
				continue
			}
			var readyPayload map[string]string
			if err := json.Unmarshal([]byte(event.Payload), &readyPayload); err != nil {
				continue
			}
			streamID := readyPayload["stream_id"]
			if !streamSet[streamID] {
				continue
			}
			stream, err := c.streams.Get(ctx, streamID)
			if err != nil || stream.Status != "pending" {
				continue
			}
			if err := c.startStreamSubExecution(ctx, exec, refBP, stream, plan.ID, results); err != nil {
				c.logger.Error("failed to start cascade sub-execution",
					"stream_id", streamID,
					"error", err,
				)
				results <- streamResult{StreamID: streamID, Error: fmt.Sprintf("failed to start: %v", err)}
			}
		}
	}

	// 6. Determine final result.
	if len(failures) > 0 {
		return blueprint.StepResult{
			Status:   blueprint.StepStatusCompleted,
			Output:   fmt.Sprintf("partial: %d/%d streams completed; failures: %s", totalStreams-len(failures), totalStreams, strings.Join(failures, "; ")),
			Metadata: map[string]string{"partial": "true", "failures": fmt.Sprintf("%d", len(failures))},
		}, nil
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// resolveBlueprint resolves a blueprint ref (e.g., ".deck/blueprints/stream.yaml")
// to a loaded blueprint by trying the ref as-is and by base filename alias.
func (c *Coordinator) resolveBlueprint(ref string) *blueprint.Blueprint {
	if bp, ok := c.engine.GetBlueprint(ref); ok {
		return bp
	}
	// Try base filename without extension: ".deck/blueprints/stream.yaml" → "stream"
	base := strings.TrimSuffix(filepath.Base(ref), filepath.Ext(ref))
	if bp, ok := c.engine.GetBlueprint(base); ok {
		return bp
	}
	return nil
}

// startStreamSubExecution creates a sub-execution for a stream and launches
// advanceSubExecution in a goroutine.
func (c *Coordinator) startStreamSubExecution(
	ctx context.Context,
	parentExec *blueprint.Execution,
	refBP *blueprint.Blueprint,
	stream *domain.Stream,
	planID string,
	results chan<- streamResult,
) error {
	if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
		return fmt.Errorf("marking stream executing: %w", err)
	}

	// Create sub-execution.
	subExec, err := c.engine.Start(ctx, refBP.Name, parentExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("starting sub-execution: %w", err)
	}
	subExec.ParentID = parentExec.ID
	subExec.StreamID = stream.ID

	if err := c.executions.Create(ctx, subExec); err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("persisting sub-execution: %w", err)
	}

	// Link stream to sub-execution.
	if err := c.streams.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		c.logger.Warn("failed to link stream to sub-execution",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
			"error", err,
		)
	}

	c.logger.Info("started stream sub-execution",
		"stream_id", stream.ID,
		"sub_execution_id", subExec.ID,
		"blueprint", refBP.Name,
		"parent_execution_id", parentExec.ID,
	)

	go c.advanceSubExecution(ctx, subExec, stream, planID, results)
	return nil
}

// advanceSubExecution drives a stream's sub-execution to completion in a goroutine.
// Reports the result via the results channel.
func (c *Coordinator) advanceSubExecution(
	ctx context.Context,
	subExec *blueprint.Execution,
	stream *domain.Stream,
	planID string,
	results chan<- streamResult,
) {
	for {
		if ctx.Err() != nil {
			results <- streamResult{StreamID: stream.ID, Error: "context cancelled"}
			return
		}

		updated, err := c.engine.Advance(ctx, subExec)
		if err != nil {
			if ctx.Err() != nil {
				results <- streamResult{StreamID: stream.ID, Error: "context cancelled"}
				return
			}
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
			results <- streamResult{StreamID: stream.ID, Error: fmt.Sprintf("advance failed: %v", err)}
			return
		}
		subExec = updated

		// Persist state after each advance.
		if err := c.executions.Update(ctx, subExec); err != nil {
			c.logger.Error("failed to persist sub-execution state",
				"execution_id", subExec.ID,
				"stream_id", stream.ID,
				"error", err,
			)
		}

		switch subExec.Status {
		case "completed":
			if err := c.scheduler.MarkCompleted(ctx, stream.ID, planID); err != nil {
				c.logger.Error("failed to mark stream completed",
					"stream_id", stream.ID,
					"error", err,
				)
			}
			results <- streamResult{StreamID: stream.ID}
			return
		case "failed":
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
			failErr := "sub-execution failed"
			if subExec.CurrentStep != "" {
				if state := subExec.StepStates[subExec.CurrentStep]; state != nil && state.Error != "" {
					failErr = state.Error
				}
			}
			// Find the last failed step's error from step states.
			for _, state := range subExec.StepStates {
				if state.Status == blueprint.StepStatusFailed && state.Error != "" {
					failErr = state.Error
					break
				}
			}
			results <- streamResult{StreamID: stream.ID, Error: failErr}
			return
		case "waiting_human":
			results <- streamResult{StreamID: stream.ID, Error: "human step in sub-execution not yet supported"}
			return
		}
		// "running" — loop continues; Advance blocks internally for agent steps.
	}
}

// escalateStreamFailure publishes an EventEscalation with structured context
// about a stream failure so that @human can review and potentially retry.
func (c *Coordinator) escalateStreamFailure(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step, streamID, errMsg string) {
	stream, err := c.streams.Get(ctx, streamID)
	if err != nil {
		c.logger.Error("failed to load stream for escalation",
			"stream_id", streamID,
			"error", err,
		)
		return
	}

	// Determine context level from escalation config.
	contextLevel := "full"
	prompt := "How should the agent resolve this?"
	if step.Escalation != nil {
		contextLevel = step.Escalation.EffectiveContext()
		prompt = step.Escalation.EffectivePrompt()
	}

	payload := map[string]string{
		"stream_id":     streamID,
		"stream_title":  stream.Title,
		"execution_id":  exec.ID,
		"error":         errMsg,
		"context_level": contextLevel,
		"prompt":        prompt,
	}
	if stream.ExecutionID != "" {
		payload["sub_execution_id"] = stream.ExecutionID
	}
	payloadJSON, _ := json.Marshal(payload)

	// Route through mail broker so the escalation is persisted and visible
	// via `deck mail` / unread-mail APIs, not just the event stream.
	if c.mailSender != nil {
		msg := &domain.MailMessage{
			From:      "coordinator",
			To:        "@human",
			Subject:   "Stream failed: " + stream.Title,
			Body:      errMsg,
			Type:      "escalation",
			Priority:  "high",
			Payload:   string(payloadJSON),
			Objective: exec.ObjectiveID,
			Stream:    streamID,
		}
		if err := c.mailSender.Send(ctx, msg); err != nil {
			c.logger.Error("failed to send stream failure escalation via broker", "error", err)
			// Fall back to direct event publish
			c.eventBus.Publish(domain.Event{
				Type:      domain.EventEscalation,
				Objective: exec.ObjectiveID,
				Stream:    streamID,
				Payload:   string(payloadJSON),
				CreatedAt: time.Now(),
			})
		}
	} else {
		c.eventBus.Publish(domain.Event{
			Type:      domain.EventEscalation,
			Objective: exec.ObjectiveID,
			Stream:    streamID,
			Payload:   string(payloadJSON),
			CreatedAt: time.Now(),
		})
	}

	c.logger.Info("stream failure escalated to human",
		"stream_id", streamID,
		"stream_title", stream.Title,
		"context_level", contextLevel,
		"execution_id", exec.ID,
	)
}

// RetryStreamExecution retries a failed stream's sub-execution with optional
// human guidance. Creates a fresh sub-execution of the same blueprint and
// injects the previous error + guidance as fix_context on the first agent step.
func (c *Coordinator) RetryStreamExecution(ctx context.Context, failedExecID string, guidance string) error {
	// 1. Get the failed sub-execution.
	failedExec, err := c.executions.Get(ctx, failedExecID)
	if err != nil {
		return fmt.Errorf("getting execution %s: %w", failedExecID, err)
	}
	if failedExec.Status != "failed" {
		return fmt.Errorf("execution %s is not failed (status: %s)", failedExecID, failedExec.Status)
	}
	if failedExec.StreamID == "" {
		return fmt.Errorf("execution %s is not a stream sub-execution", failedExecID)
	}

	// 2. Get the stream and verify it's failed.
	stream, err := c.streams.Get(ctx, failedExec.StreamID)
	if err != nil {
		return fmt.Errorf("getting stream %s: %w", failedExec.StreamID, err)
	}
	if stream.Status != "failed" {
		return fmt.Errorf("stream %s is not failed (status: %s)", stream.ID, stream.Status)
	}

	// 3. Resolve the blueprint used by the failed sub-execution.
	refBP, ok := c.engine.GetBlueprint(failedExec.BlueprintName)
	if !ok {
		return fmt.Errorf("blueprint %q not found", failedExec.BlueprintName)
	}

	// 4. Collect the last error from the failed execution for fix context.
	var lastError string
	for _, state := range failedExec.StepStates {
		if state.Status == blueprint.StepStatusFailed && state.Error != "" {
			lastError = state.Error
			break
		}
	}

	// 5. Build combined fix context from error + human guidance.
	var fixContext strings.Builder
	if lastError != "" {
		fmt.Fprintf(&fixContext, "Previous error:\n%s\n", lastError)
	}
	if guidance != "" {
		fmt.Fprintf(&fixContext, "\nHuman guidance:\n%s\n", guidance)
	}

	// 6. Reset stream to pending.
	if err := c.streams.UpdateStatus(ctx, stream.ID, "pending"); err != nil {
		return fmt.Errorf("resetting stream %s to pending: %w", stream.ID, err)
	}
	if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
		return fmt.Errorf("marking stream %s executing: %w", stream.ID, err)
	}

	// 7. Create a new sub-execution.
	subExec, err := c.engine.Start(ctx, refBP.Name, failedExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("starting retry sub-execution: %w", err)
	}
	subExec.ParentID = failedExec.ParentID
	subExec.StreamID = stream.ID

	// 8. Inject fix context on the first agent step.
	if fixCtx := fixContext.String(); fixCtx != "" {
		for _, step := range refBP.Steps {
			if step.Type == blueprint.StepTypeAgent {
				if state := subExec.StepStates[step.ID]; state != nil {
					state.Metadata = map[string]string{
						"fix_context": fixCtx,
					}
				}
				break
			}
		}
	}

	if err := c.executions.Create(ctx, subExec); err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("persisting retry sub-execution: %w", err)
	}

	// Link stream to new sub-execution.
	if err := c.streams.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		c.logger.Warn("failed to link stream to retry sub-execution",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
			"error", err,
		)
	}

	c.logger.Info("retrying stream sub-execution",
		"stream_id", stream.ID,
		"failed_execution_id", failedExecID,
		"new_execution_id", subExec.ID,
		"has_guidance", guidance != "",
	)

	// 9. Get the plan ID for MarkCompleted cascade.
	plan, err := c.plans.GetByObjective(ctx, failedExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("getting plan for objective: %w", err)
	}

	// 10. Launch the sub-execution in a goroutine and handle completion.
	baseCtx := c.ctx
	if baseCtx == nil {
		baseCtx = ctx
	}
	execCtx, cancel := context.WithCancel(baseCtx)

	go func() {
		defer cancel()

		results := make(chan streamResult, 1)
		c.advanceSubExecution(execCtx, subExec, stream, plan.ID, results)
		res := <-results

		if res.Error != "" {
			c.logger.Warn("retry sub-execution failed",
				"stream_id", stream.ID,
				"execution_id", subExec.ID,
				"error", res.Error,
			)
			return
		}

		c.logger.Info("retry sub-execution completed",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
		)

		// Enqueue the retried stream for merge (the parent merge step already ran).
		if c.mergeEnqueuer != nil {
			if err := c.mergeEnqueuer.EnqueueStream(execCtx, stream.ID); err != nil {
				c.logger.Warn("failed to enqueue retried stream for merge",
					"stream_id", stream.ID,
					"error", err,
				)
			}
		}

		// Check if all streams are now completed — upgrade objective from partial to completed.
		c.checkPartialToCompleted(execCtx, failedExec.ObjectiveID)
	}()

	return nil
}

// checkPartialToCompleted checks if an objective in "partial" status can be
// upgraded to "completed" (all streams now completed).
func (c *Coordinator) checkPartialToCompleted(ctx context.Context, objectiveID string) {
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil || obj.Status != domain.ObjectiveStatusPartial {
		return
	}

	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return
	}
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return
	}

	for _, s := range streams {
		if s.Status != "completed" && s.Status != domain.StreamStatusMerged {
			return // still have non-terminal streams (merge_ready/merging not final)
		}
	}

	// All streams are fully merged or completed.
	// Re-push the merger branch first — if push fails, stay partial so the
	// user can retry. Only transition to completed if push succeeds (or if
	// no remote is configured, in which case push is skipped).
	if !lifecycle.IsValidTransition(obj.Status, domain.ObjectiveStatusCompleted) {
		return
	}

	if !c.rePushMergerBranch(ctx, objectiveID) {
		c.logger.Warn("staying partial — merger branch re-push failed, retry possible",
			"objective_id", objectiveID,
		)
		return
	}

	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusCompleted); err != nil {
		c.logger.Warn("failed to upgrade objective from partial to completed",
			"objective_id", objectiveID,
			"error", err,
		)
	} else {
		c.logger.Info("objective upgraded from partial to completed",
			"objective_id", objectiveID,
		)
		c.cleanupObjectiveSandboxes(ctx, objectiveID)
	}
}

// rePushMergerBranch pushes the merger branch to origin after a retry completes.
// Returns true if push succeeded or was skipped (no remote), false on push failure.
func (c *Coordinator) rePushMergerBranch(ctx context.Context, objectiveID string) bool {
	sb, err := c.spawner.FindMergerSandbox(ctx, objectiveID)
	if err != nil || sb == nil {
		// No merger sandbox — likely a simple/hotfix objective with no merge step.
		return true
	}

	// Check if origin remote exists — if not, push is not applicable.
	remoteCheck, err := sb.Exec(ctx, "git remote get-url origin", sandbox.ExecOpts{})
	if err != nil || remoteCheck.ExitCode != 0 {
		return true // no remote configured, nothing to push
	}

	branchResult, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil || branchResult.ExitCode != 0 {
		c.logger.Warn("failed to get merger branch name", "objective", objectiveID)
		return false
	}
	branch := strings.TrimSpace(branchResult.Stdout)

	pushResult, err := sb.Exec(ctx, fmt.Sprintf("git push -f origin %s", branch), sandbox.ExecOpts{})
	if err != nil || pushResult.ExitCode != 0 {
		c.logger.Warn("failed to re-push merger branch after retry",
			"objective", objectiveID,
			"branch", branch,
			"error", pushResult.Stderr,
		)
		return false
	}

	c.logger.Info("re-pushed merger branch after retry",
		"objective", objectiveID,
		"branch", branch,
	)
	return true
}

// spawnAndMonitor spawns an agent and launches a background goroutine that waits
// for completion and publishes EventAgentCompleted or EventAgentFailed via the spawner.
func (c *Coordinator) spawnAndMonitor(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	result, err := c.spawner.Spawn(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("spawning agent: %w", err)
	}

	c.mu.Lock()
	c.agentMap[result.Session.ID] = result
	delete(c.terminated, result.Session.ID)
	c.mu.Unlock()

	// Drain agent activity events to logger + event bus.
	c.drainAgentActivity(result.Session, result.Process)

	go func() {
		agentResult, waitErr := result.Process.Wait()
		wasKilled := c.finishTrackedAgent(result.Session.ID)

		if wasKilled {
			c.spawner.MarkFailed(ctx, result.Session, "killed")
			return
		}
		if waitErr != nil || !agentResult.Success {
			errMsg := agentResult.Error
			if waitErr != nil {
				errMsg = waitErr.Error()
			}
			c.spawner.MarkFailed(ctx, result.Session, errMsg)
			return
		}
		c.spawner.MarkCompleted(ctx, result.Session, agentResult.Summary)
	}()

	return result, nil
}

// KillAgent terminates a live agent process when possible and marks its session failed.
func (c *Coordinator) KillAgent(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	result, ok := c.agentMap[sessionID]
	if ok {
		c.terminated[sessionID] = true
	}
	c.mu.Unlock()

	if !ok {
		return c.spawner.Kill(ctx, sessionID)
	}

	if err := result.Process.Kill(); err != nil {
		return fmt.Errorf("killing agent process %s: %w", sessionID, err)
	}
	c.spawner.MarkFailed(ctx, result.Session, "killed")
	return nil
}

func (c *Coordinator) finishTrackedAgent(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.agentMap, sessionID)
	wasKilled := c.terminated[sessionID]
	delete(c.terminated, sessionID)
	return wasKilled
}

func (c *Coordinator) singleStreamForObjective(ctx context.Context, objectiveID string) (*domain.Stream, error) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	if len(streams) != 1 {
		return nil, nil
	}
	stream := streams[0]
	return &stream, nil
}

func (c *Coordinator) extractGeneratedMessages(step *blueprint.Step, summary string) (string, agents.GeneratedMessages) {
	if step.Messages == nil || !step.Messages.Any() {
		return summary, agents.GeneratedMessages{}
	}

	cleanSummary, msgs, found, err := agents.ExtractGeneratedMessages(summary)
	if err != nil {
		c.logger.Warn("agent produced invalid generated messages output",
			"step", step.ID,
			"error", err,
		)
		return cleanSummary, agents.GeneratedMessages{}
	}
	if !found {
		c.logger.Warn("agent did not emit generated messages output",
			"step", step.ID,
		)
		return cleanSummary, agents.GeneratedMessages{}
	}
	return cleanSummary, msgs
}

// autoCommit stages and commits all changes in a sandbox worktree.
// The commit message is taken from the agent when provided, otherwise it falls
// back to the objective description plus diff stat.
func (c *Coordinator) autoCommit(ctx context.Context, sb sandbox.Sandbox, objectiveDesc, generatedCommitMessage string) error {
	// Check if there are uncommitted changes.
	statusResult, err := sb.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(statusResult.Stdout) == "" {
		c.logger.Info("no uncommitted changes, skipping auto-commit")
		return nil
	}

	commitMessage := strings.TrimSpace(generatedCommitMessage)
	if commitMessage == "" {
		// Get diff stat for the commit message body.
		diffResult, _ := sb.Exec(ctx, "git diff --stat", sandbox.ExecOpts{})

		// Build commit message.
		var msg strings.Builder
		fmt.Fprintf(&msg, "deck: %s", objectiveDesc)
		if strings.TrimSpace(diffResult.Stdout) != "" {
			fmt.Fprintf(&msg, "\n\n%s", strings.TrimSpace(diffResult.Stdout))
		}
		commitMessage = msg.String()
	}

	// Stage all changes before uploading the temporary commit-message file so it
	// never becomes part of the commit.
	if stageResult, err := sb.Exec(ctx, "git add -A", sandbox.ExecOpts{}); err != nil {
		return fmt.Errorf("staging changes: %w (stderr: %s)", err, stageResult.Stderr)
	}

	const commitMessagePath = ".deck/tmp/auto-commit-message.txt"
	if err := sb.Upload(ctx, []byte(commitMessage+"\n"), commitMessagePath); err != nil {
		return fmt.Errorf("uploading commit message: %w", err)
	}
	defer func() {
		_, _ = sb.Exec(context.Background(), fmt.Sprintf("rm -f '%s'", escapeShellSingleQuote(commitMessagePath)), sandbox.ExecOpts{})
	}()

	commitCmd := fmt.Sprintf("git commit -F '%s'", escapeShellSingleQuote(commitMessagePath))
	commitResult, err := sb.Exec(ctx, commitCmd, sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("committing: %w (stderr: %s)", err, commitResult.Stderr)
	}
	if commitResult.ExitCode != 0 {
		return fmt.Errorf("git commit failed (exit %d): %s", commitResult.ExitCode, commitResult.Stderr)
	}

	c.logger.Info("auto-committed agent changes")
	return nil
}

func (c *Coordinator) completeExecution(ctx context.Context, objectiveID string) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load plan on execution completion", "objective_id", objectiveID, "error", err)
		return
	}

	// Check stream statuses to determine if the outcome is partial.
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		c.logger.Error("failed to list streams on execution completion", "objective_id", objectiveID, "error", err)
	}

	hasFailedStreams := false
	for _, s := range streams {
		if s.Status == "failed" {
			hasFailedStreams = true
			break
		}
	}

	// For single-stream executions, mark the stream completed if still active.
	if len(streams) == 1 {
		s := streams[0]
		if s.Status == "pending" || s.Status == "executing" {
			if err := c.scheduler.MarkCompleted(ctx, s.ID, plan.ID); err != nil {
				c.logger.Error("failed to mark single stream completed", "stream_id", s.ID, "error", err)
			}
		}
	}

	// Determine target status: partial if some streams failed, completed otherwise.
	targetStatus := domain.ObjectiveStatusCompleted
	planStatus := domain.PlanStatusCompleted
	if hasFailedStreams {
		targetStatus = domain.ObjectiveStatusPartial
		// Plan is still "completed" — the execution finished, some streams just failed.
	}

	if err := c.plans.UpdateStatus(ctx, plan.ID, planStatus); err != nil {
		c.logger.Error("failed to mark plan completed", "plan_id", plan.ID, "error", err)
	}

	// Safety net: ensure the objective reaches the right terminal state even if
	// mark_complete already handled it (Transition is a no-op if status is already terminal).
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load objective on execution completion", "objective_id", objectiveID, "error", err)
		return
	}
	if obj.Status != domain.ObjectiveStatusCompleted && obj.Status != domain.ObjectiveStatusFailed && obj.Status != domain.ObjectiveStatusPartial {
		if lifecycle.IsValidTransition(obj.Status, targetStatus) {
			if err := c.lifecycle.Transition(ctx, objectiveID, targetStatus); err != nil {
				c.logger.Warn("safety-net objective completion transition failed",
					"objective_id", objectiveID,
					"current_status", obj.Status,
					"target_status", targetStatus,
					"error", err,
				)
			}
		}
	}

	// Clean up sandboxes only when fully completed — partial objectives
	// retain their sandboxes so failed streams can be retried and merged.
	if targetStatus == domain.ObjectiveStatusCompleted {
		c.cleanupObjectiveSandboxes(ctx, objectiveID)
	}
}

func (c *Coordinator) failExecution(ctx context.Context, objectiveID, reason string) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err == nil {
		if err := c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusFailed); err != nil {
			c.logger.Error("failed to mark plan failed", "plan_id", plan.ID, "error", err)
		}
	} else {
		c.logger.Error("failed to load plan on execution failure", "objective_id", objectiveID, "error", err)
	}

	stream, streamErr := c.singleStreamForObjective(ctx, objectiveID)
	if streamErr != nil {
		c.logger.Error("failed to load single stream on execution failure", "objective_id", objectiveID, "error", streamErr)
	} else if stream != nil && (stream.Status == "pending" || stream.Status == "executing") {
		if err := c.scheduler.MarkFailed(ctx, stream.ID); err != nil {
			c.logger.Error("failed to mark single stream failed", "stream_id", stream.ID, "error", err)
		}
	}

	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load objective on execution failure", "objective_id", objectiveID, "error", err)
		return
	}
	if obj.Status != domain.ObjectiveStatusFailed && lifecycle.IsValidTransition(obj.Status, domain.ObjectiveStatusFailed) {
		if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusFailed); err != nil {
			c.logger.Error("failed to transition objective to failed", "objective_id", objectiveID, "reason", reason, "error", err)
		}
	}

	// Clean up sandboxes and branches for this objective.
	c.cleanupObjectiveSandboxes(ctx, objectiveID)
}

// drainAgentActivity starts a goroutine that reads agent output events and
// publishes them to the event bus + writes to the activity JSONL log.
// cleanupObjectiveSandboxes removes all sandboxes and branches for a completed objective.
// Runs in a background goroutine to avoid blocking the completion flow.
func (c *Coordinator) cleanupObjectiveSandboxes(ctx context.Context, objectiveID string) {
	go func() {
		c.spawner.CleanupObjective(ctx, objectiveID)
	}()
}

// Also starts timeout timers (max duration + idle) for the agent.
// Returns immediately — the goroutine runs until the output channel closes.
func (c *Coordinator) drainAgentActivity(session *domain.AgentSession, process interface {
	Output() <-chan runtime.AgentEvent
	Kill() error
}) {
	role := string(session.Role)
	rt := c.timeouts.GetTimeout(role)

	// Start max duration timer
	if rt.MaxDurationMinutes > 0 {
		dur := time.Duration(rt.MaxDurationMinutes) * time.Minute
		time.AfterFunc(dur, func() {
			c.mu.Lock()
			_, stillActive := c.agentMap[session.ID]
			if stillActive {
				c.terminated[session.ID] = true
			}
			c.mu.Unlock()
			if stillActive {
				c.logger.Warn("agent max duration exceeded, killing",
					"session_id", session.ID,
					"role", role,
					"max_minutes", rt.MaxDurationMinutes,
				)
				_ = process.Kill()
			}
		})
	}

	// Start idle timer
	var idleTimer *time.Timer
	if rt.IdleMinutes > 0 {
		idleDur := time.Duration(rt.IdleMinutes) * time.Minute
		idleTimer = time.AfterFunc(idleDur, func() {
			c.mu.Lock()
			_, stillActive := c.agentMap[session.ID]
			if stillActive {
				c.terminated[session.ID] = true
			}
			c.mu.Unlock()
			if stillActive {
				c.logger.Warn("agent idle timeout, killing",
					"session_id", session.ID,
					"role", role,
					"idle_minutes", rt.IdleMinutes,
				)
				_ = process.Kill()
			}
		})
		c.mu.Lock()
		c.idleTimers[session.ID] = idleTimer
		c.mu.Unlock()
	}

	go func() {
		for event := range process.Output() {
			// Reset idle timer on any activity
			if idleTimer != nil {
				idleTimer.Reset(time.Duration(rt.IdleMinutes) * time.Minute)
			}

			kind := event.Type
			tool := ""
			switch event.Type {
			case "tool_call":
				kind = "tool_start"
				if idx := strings.Index(event.Content, ": "); idx > 0 {
					tool = event.Content[:idx]
				} else {
					tool = event.Content
				}
			case "tool_end":
				kind = "tool_end"
				tool = strings.TrimSuffix(event.Content, " (failed)")
			case "output":
				kind = "message"
			case "error":
				kind = "error"
			}

			if c.activityLogger != nil {
				c.activityLogger.Log(agents.ActivityEvent{
					Timestamp: time.Now(),
					AgentID:   session.ID,
					Kind:      kind,
					Tool:      tool,
					Content:   event.Content,
					IsError:   event.IsError,
				})
			}

			payload, _ := json.Marshal(map[string]any{
				"kind":     kind,
				"tool":     tool,
				"content":  truncateForEvent(event.Content, 500),
				"is_error": event.IsError,
			})
			c.eventBus.Publish(domain.Event{
				Type:      domain.EventAgentActivity,
				Objective: session.ObjectiveID,
				Stream:    session.StreamID,
				Agent:     session.ID,
				Payload:   string(payload),
				CreatedAt: time.Now(),
			})
		}

		// Cleanup: stop idle timer and close log file
		if idleTimer != nil {
			idleTimer.Stop()
			c.mu.Lock()
			delete(c.idleTimers, session.ID)
			c.mu.Unlock()
		}
		if c.activityLogger != nil {
			c.activityLogger.CloseAgent(session.ID)
		}
	}()
}

func truncateForEvent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
