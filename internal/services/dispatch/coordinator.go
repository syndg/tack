package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/sandbox"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// Coordinator orchestrates objective execution from plan approval to completion.
// It subscribes to events and drives blueprint execution.
type Coordinator struct {
	engine     *blueprint.Engine
	scheduler  *Scheduler
	spawner    *Spawner
	lifecycle  *lifecycle.Manager
	executions *db.ExecutionStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	eventBus   *events.PersistentBus
	logger     *slog.Logger

	ctx         context.Context // set in Start(); used as parent for execution goroutines
	mu          sync.Mutex
	activeExecs map[string]context.CancelFunc // objectiveID → cancel
	agentMap    map[string]*SpawnResult       // sessionID → spawn result
	terminated  map[string]bool               // sessionID → explicitly killed
}

// NewCoordinator creates a new Coordinator and registers step handlers with the engine.
func NewCoordinator(
	engine *blueprint.Engine,
	scheduler *Scheduler,
	spawner *Spawner,
	lc *lifecycle.Manager,
	executions *db.ExecutionStore,
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Coordinator {
	c := &Coordinator{
		engine:      engine,
		scheduler:   scheduler,
		spawner:     spawner,
		lifecycle:   lc,
		executions:  executions,
		objectives:  objectives,
		plans:       plans,
		streams:     streams,
		eventBus:    eventBus,
		logger:      logger,
		activeExecs: make(map[string]context.CancelFunc),
		agentMap:    make(map[string]*SpawnResult),
		terminated:  make(map[string]bool),
	}

	// Register step handlers with the engine.
	engine.RegisterHandler(blueprint.StepTypeAgent, c.HandleAgentStep)
	engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)

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
				case domain.EventObjectiveUpdated:
					c.handleObjectiveUpdated(ctx, event)
				}
			}
		}
	}()

	c.logger.Info("coordinator started")
	return nil
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

// handleObjectiveUpdated processes EventObjectiveUpdated events.
// Triggers StartExecution when an objective transitions to "approved".
func (c *Coordinator) handleObjectiveUpdated(ctx context.Context, event domain.Event) {
	var payload map[string]string
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		c.logger.Error("failed to parse objective updated payload", "error", err)
		return
	}

	if payload["to"] != string(domain.ObjectiveStatusApproved) {
		return
	}

	if event.Objective == "" {
		return
	}

	go func() {
		if err := c.StartExecution(ctx, event.Objective); err != nil {
			c.logger.Error("failed to start execution after approval",
				"objective_id", event.Objective,
				"error", err,
			)
		}
	}()
}

// StartExecution begins blueprint execution for an approved objective.
//  1. Fetch objective, verify status is "approved"
//  2. Fetch plan for the objective
//  3. Determine blueprint name (from objective.Blueprint, default to "feature")
//  4. Create execution via engine.Start(blueprintName, objectiveID)
//  5. Transition objective to "executing" via lifecycle
//  6. Run the execution loop in a goroutine
func (c *Coordinator) StartExecution(ctx context.Context, objectiveID string) error {
	// 1. Fetch objective and verify status is "approved".
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective %s: %w", objectiveID, err)
	}
	if obj.Status != domain.ObjectiveStatusApproved {
		return fmt.Errorf("objective %s is not approved (status: %s)", objectiveID, obj.Status)
	}

	// 2. Fetch plan for the objective.
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting plan for objective %s: %w", objectiveID, err)
	}

	// 3. Determine blueprint name, defaulting to the shipped feature blueprint.
	blueprintName := obj.Blueprint
	if blueprintName == "" {
		blueprintName = "Feature Implementation"
	}

	// 4. Create execution via engine.Start.
	exec, err := c.engine.Start(ctx, blueprintName, objectiveID)
	if err != nil {
		return fmt.Errorf("starting blueprint execution for objective %s: %w", objectiveID, err)
	}
	c.prepareExecutionForApprovedObjective(exec)
	if err := c.executions.Create(ctx, exec); err != nil {
		return fmt.Errorf("persisting execution for objective %s: %w", objectiveID, err)
	}

	// 5. Transition objective and plan to executing.
	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusExecuting); err != nil {
		return fmt.Errorf("transitioning objective %s to executing: %w", objectiveID, err)
	}
	if err := c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusExecuting); err != nil {
		return fmt.Errorf("transitioning plan %s to executing: %w", plan.ID, err)
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

	stream, err := c.singleStreamForObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting stream context: %s", err),
		}, nil
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

	// Spawn the agent.
	result, err := c.spawner.Spawn(ctx, SpawnRequest{
		Objective:  obj,
		Stream:     stream,
		Role:       role,
		TaskSpec:   taskSpec,
		CommitMode: string(step.EffectiveCommitMode()),
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

	// Handle commit mode after successful agent completion.
	commitMode := step.EffectiveCommitMode()
	switch commitMode {
	case blueprint.CommitModeAuto:
		if err := c.autoCommit(ctx, result.Sandbox, obj.Description); err != nil {
			c.logger.Error("auto-commit failed", "step", step.ID, "error", err)
			// Non-fatal: changes are still in the worktree for the merge processor.
		}
	case blueprint.CommitModeAgent:
		// Verify the agent actually committed; fall back to auto if not.
		statusResult, err := result.Sandbox.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
		if err == nil && strings.TrimSpace(statusResult.Stdout) != "" {
			c.logger.Warn("agent mode set but uncommitted changes found, falling back to auto-commit", "step", step.ID)
			if err := c.autoCommit(ctx, result.Sandbox, obj.Description); err != nil {
				c.logger.Error("fallback auto-commit failed", "step", step.ID, "error", err)
			}
		}
	case blueprint.CommitModeNone:
		// No commit needed.
	}

	c.spawner.MarkCompleted(ctx, result.Session, agentResult.Summary)
	return blueprint.StepResult{
		Status: blueprint.StepStatusCompleted,
		Output: agentResult.Summary,
	}, nil
}

// HandleBlueprintRefStep implements the StepHandler for blueprint_ref steps.
// Registered with the engine as the StepTypeBlueprintRef handler.
// For Phase 4, this handles per-stream execution without nested blueprints:
//  1. Get the plan and all its streams
//  2. Spawn lead agents for all ready streams (via spawner)
//  3. Monitor agent completions via event subscription
//  4. As leads complete: mark stream completed via scheduler, spawn newly ready leads
//  5. Block until ALL streams are completed
//  6. Return StepResult{Status: "completed"}
func (c *Coordinator) HandleBlueprintRefStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	// 1. Get the plan and all its streams.
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

	// Build a set of stream IDs for event filtering and pre-count already completed.
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

	obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	// Subscribe to events BEFORE spawning to avoid missing completions.
	sub, unsub := c.eventBus.Subscribe(128)
	defer unsub()

	// 2. Spawn lead agents for all initially ready streams.
	ready, err := c.scheduler.GetReadyStreams(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting ready streams: %s", err),
		}, nil
	}

	for i := range ready {
		stream := ready[i]
		if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
			c.logger.Warn("failed to mark stream executing",
				"stream_id", stream.ID,
				"error", err,
			)
			continue
		}
		if _, err := c.spawnAndMonitor(ctx, SpawnRequest{
			Objective: obj,
			Stream:    &stream,
			Role:      "lead",
			TaskSpec:  stream.Description,
		}); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("spawning lead for stream %s: %s", stream.ID, err),
			}, nil
		}
	}

	// 4. Wait for events until all streams complete or one fails.
	for completedCount < totalStreams {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  "execution context cancelled",
			}, nil

		case event, ok := <-sub:
			if !ok {
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  "event subscription closed unexpectedly",
				}, nil
			}

			switch event.Type {
			case domain.EventAgentCompleted:
				if !streamSet[event.Stream] {
					continue // Not one of our streams.
				}
				if err := c.scheduler.MarkCompleted(ctx, event.Stream, plan.ID); err != nil {
					c.logger.Error("failed to mark stream completed",
						"stream_id", event.Stream,
						"error", err,
					)
				}
				completedCount++
				c.logger.Info("stream agent completed",
					"stream_id", event.Stream,
					"completed", completedCount,
					"total", totalStreams,
				)

			case domain.EventAgentFailed:
				if !streamSet[event.Stream] {
					continue
				}
				if err := c.scheduler.MarkFailed(ctx, event.Stream); err != nil {
					c.logger.Error("failed to mark stream failed",
						"stream_id", event.Stream,
						"error", err,
					)
				}
				var failPayload map[string]string
				_ = json.Unmarshal([]byte(event.Payload), &failPayload)
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  fmt.Sprintf("stream %s agent failed: %s", event.Stream, failPayload["reason"]),
				}, nil

			case domain.EventStreamReady:
				// A previously blocked stream is now ready — spawn a lead for it.
				var readyPayload map[string]string
				if err := json.Unmarshal([]byte(event.Payload), &readyPayload); err != nil {
					continue
				}
				streamID := readyPayload["stream_id"]
				if !streamSet[streamID] {
					continue // Not our plan.
				}

				stream, err := c.streams.Get(ctx, streamID)
				if err != nil {
					c.logger.Error("failed to get cascade stream", "stream_id", streamID, "error", err)
					continue
				}

				// Only spawn if pending; skip if already claimed by HandleBlueprintRefStep.
				if stream.Status != "pending" {
					continue
				}

				if err := c.scheduler.MarkExecuting(ctx, streamID); err != nil {
					c.logger.Warn("failed to mark cascade stream executing",
						"stream_id", streamID,
						"error", err,
					)
					continue
				}
				if _, err := c.spawnAndMonitor(ctx, SpawnRequest{
					Objective: obj,
					Stream:    stream,
					Role:      "lead",
					TaskSpec:  stream.Description,
				}); err != nil {
					c.logger.Error("failed to spawn lead for cascade stream",
						"stream_id", streamID,
						"error", err,
					)
				}
			}
		}
	}

	// 6. All streams completed.
	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
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

func (c *Coordinator) prepareExecutionForApprovedObjective(exec *blueprint.Execution) {
	bp, ok := c.engine.GetBlueprint(exec.BlueprintName)
	if !ok || len(bp.Steps) == 0 {
		return
	}

	first := bp.Steps[0]
	if first.Type != blueprint.StepTypeAgent || first.Role != string(domain.AgentRolePlanner) {
		return
	}

	now := time.Now()
	if state := exec.StepStates[first.ID]; state != nil {
		state.Status = blueprint.StepStatusCompleted
		state.Error = ""
	}

	nextID := first.Next
	if nextID != "" {
		if step, err := c.engine.GetStepByID(bp, nextID); err == nil && step.Type == blueprint.StepTypeHuman {
			if state := exec.StepStates[step.ID]; state != nil {
				state.Status = blueprint.StepStatusCompleted
				state.Error = ""
			}
			nextID = step.Next
		}
	}

	exec.CurrentStep = nextID
	exec.UpdatedAt = now
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

// autoCommit stages and commits all changes in a sandbox worktree.
// The commit message is derived from the objective description and diff stat.
func (c *Coordinator) autoCommit(ctx context.Context, sb sandbox.Sandbox, objectiveDesc string) error {
	// Check if there are uncommitted changes.
	statusResult, err := sb.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(statusResult.Stdout) == "" {
		c.logger.Info("no uncommitted changes, skipping auto-commit")
		return nil
	}

	// Get diff stat for the commit message body.
	diffResult, _ := sb.Exec(ctx, "git diff --stat", sandbox.ExecOpts{})

	// Build commit message.
	var msg strings.Builder
	fmt.Fprintf(&msg, "deck: %s", objectiveDesc)
	if strings.TrimSpace(diffResult.Stdout) != "" {
		fmt.Fprintf(&msg, "\n\n%s", strings.TrimSpace(diffResult.Stdout))
	}

	// Stage all changes.
	if stageResult, err := sb.Exec(ctx, "git add -A", sandbox.ExecOpts{}); err != nil {
		return fmt.Errorf("staging changes: %w (stderr: %s)", err, stageResult.Stderr)
	}

	// Commit with shell-escaped message.
	escaped := strings.ReplaceAll(msg.String(), "'", `'\''`)
	commitCmd := fmt.Sprintf("git commit -m '%s'", escaped)
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
	if err := c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusCompleted); err != nil {
		c.logger.Error("failed to mark plan completed", "plan_id", plan.ID, "error", err)
	}

	stream, err := c.singleStreamForObjective(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load single stream on execution completion", "objective_id", objectiveID, "error", err)
		return
	}
	if stream != nil && (stream.Status == "pending" || stream.Status == "executing") {
		if err := c.scheduler.MarkCompleted(ctx, stream.ID, plan.ID); err != nil {
			c.logger.Error("failed to mark single stream completed", "stream_id", stream.ID, "error", err)
		}
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
}
