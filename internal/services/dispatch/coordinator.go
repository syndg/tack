package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/services/agents"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// MergeEnqueuer creates merge-queue entries for streams ready to merge.
type MergeEnqueuer interface {
	EnqueueStream(ctx context.Context, streamID string) error
	MergerSandboxID(objectiveID string) string
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
	c.eventBus.Emit(domain.EventExecutionStarted, objectiveID, "", "",
		"execution_id", exec.ID,
		"blueprint_name", blueprintName,
	)

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

// drainAgentActivity starts a goroutine that reads agent output events and
// publishes them to the event bus + writes to the activity JSONL log.
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

			c.eventBus.Emit(domain.EventAgentActivity, session.ObjectiveID, session.StreamID, session.ID,
				"kind", kind,
				"tool", tool,
				"content", truncateForEvent(event.Content, 500),
				"is_error", event.IsError,
			)
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
