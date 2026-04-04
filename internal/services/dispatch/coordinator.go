package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/agents"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// Sentinel errors for Orchestrator methods.
var (
	ErrNotFound      = errors.New("not found")
	ErrInvalidState  = errors.New("invalid state")
	ErrAlreadyActive = errors.New("already active")
)

// Orchestrator is the daemon's view of execution coordination.
type Orchestrator interface {
	Start(ctx context.Context) error
	Stop()
	Execute(ctx context.Context, objectiveID string) error
	Approve(ctx context.Context, executionID string) error
	Retry(ctx context.Context, executionID string, guidance string) error
	Kill(ctx context.Context, sessionID string) error
}

// Compile-time check that *Coordinator satisfies Orchestrator.
var _ Orchestrator = (*Coordinator)(nil)

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

// Config holds all dependencies for constructing a Coordinator.
type Config struct {
	ProjectID      string                 // owning project for recovery filtering
	Engine         *blueprint.Engine      // blueprint execution engine
	Scheduler      *Scheduler             // stream scheduling
	Spawner        *Spawner               // agent process spawning
	Lifecycle      *lifecycle.Manager     // objective state transitions
	MergeEnqueuer  MergeEnqueuer          // merge queue integration
	PlanCreator    PlanCreator            // plan creation from planner output
	MailSender     MailSender             // optional: nil disables mail escalation
	Executions     *db.ExecutionStore     // execution persistence
	Objectives     *db.ObjectiveStore     // objective persistence
	Plans          *db.PlanStore          // plan persistence
	Streams        *db.StreamStore        // stream persistence
	EventBus       *events.PersistentBus  // event pub/sub
	ActivityLogger *agents.ActivityLogger // optional: nil disables activity logging
	Timeouts       config.TimeoutConfig   // per-role timeout configuration
	Logger         *slog.Logger           // structured logger
}

// Validate checks that all required Config fields are set.
// Returns a descriptive error listing all missing fields.
func (c Config) Validate() error {
	var missing []string
	if c.Engine == nil {
		missing = append(missing, "Engine")
	}
	if c.Scheduler == nil {
		missing = append(missing, "Scheduler")
	}
	if c.Spawner == nil {
		missing = append(missing, "Spawner")
	}
	if c.Lifecycle == nil {
		missing = append(missing, "Lifecycle")
	}
	if c.MergeEnqueuer == nil {
		missing = append(missing, "MergeEnqueuer")
	}
	if c.PlanCreator == nil {
		missing = append(missing, "PlanCreator")
	}
	if c.Executions == nil {
		missing = append(missing, "Executions")
	}
	if c.Objectives == nil {
		missing = append(missing, "Objectives")
	}
	if c.Plans == nil {
		missing = append(missing, "Plans")
	}
	if c.Streams == nil {
		missing = append(missing, "Streams")
	}
	if c.EventBus == nil {
		missing = append(missing, "EventBus")
	}
	if c.Logger == nil {
		missing = append(missing, "Logger")
	}
	if len(missing) > 0 {
		return fmt.Errorf("dispatch.Config: missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Coordinator orchestrates objective execution from plan approval to completion.
// It subscribes to events and drives blueprint execution.
type Coordinator struct {
	engine        *blueprint.Engine
	scheduler     *Scheduler
	spawner       *Spawner
	lifecycle     *lifecycle.Manager
	mergeEnqueuer MergeEnqueuer
	planCreator   PlanCreator
	mailSender    MailSender
	executions    *db.ExecutionStore
	objectives    *db.ObjectiveStore
	plans         *db.PlanStore
	streams       *db.StreamStore
	eventBus      *events.PersistentBus
	tracker       AgentTracker
	logger        *slog.Logger
	projectID     string

	ctx         context.Context // set in Start(); used as parent for execution goroutines
	mu          sync.Mutex
	activeExecs map[string]context.CancelFunc // objectiveID → cancel
}

// NewCoordinator creates a new Coordinator, validates config, and registers
// step handlers with the blueprint engine.
func NewCoordinator(cfg Config) (*Coordinator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	c := &Coordinator{
		engine:        cfg.Engine,
		scheduler:     cfg.Scheduler,
		spawner:       cfg.Spawner,
		lifecycle:     cfg.Lifecycle,
		mergeEnqueuer: cfg.MergeEnqueuer,
		planCreator:   cfg.PlanCreator,
		mailSender:    cfg.MailSender,
		executions:    cfg.Executions,
		objectives:    cfg.Objectives,
		plans:         cfg.Plans,
		streams:       cfg.Streams,
		eventBus:      cfg.EventBus,
		tracker:       newAgentTracker(cfg.Spawner, cfg.ActivityLogger, cfg.EventBus, cfg.Timeouts, cfg.Logger),
		logger:        cfg.Logger,
		projectID:     cfg.ProjectID,
		activeExecs:   make(map[string]context.CancelFunc),
	}

	// Register step handlers with the engine.
	cfg.Engine.RegisterHandler(blueprint.StepTypeAgent, c.HandleAgentStep)
	cfg.Engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)

	return c, nil
}

// Start subscribes to events and begins processing.
// Execution is triggered exclusively through runs.Start() — the coordinator
// no longer listens for EventObjectiveCreated. It still listens for
// EventMergeCompleted to drive partial→completed upgrades on the retry path.
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
	var (
		allExecs []blueprint.Execution
		err      error
	)
	if c.projectID != "" {
		allExecs, err = c.executions.ListByProject(ctx, c.projectID)
	} else {
		allExecs, err = c.executions.List(ctx)
	}
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

// Stop cancels all active executions, kills tracked agents, and cleans up.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	for objectiveID, cancel := range c.activeExecs {
		c.logger.Info("cancelling active execution on stop", "objective_id", objectiveID)
		cancel()
	}
	c.activeExecs = make(map[string]context.CancelFunc)
	c.mu.Unlock()

	c.tracker.StopAll()
	c.logger.Info("coordinator stopped")
}

// Approve absorbs the full approval dance: fetch execution, validate status,
// approve the human step via the engine, persist, and resume the execution loop.
func (c *Coordinator) Approve(ctx context.Context, executionID string) error {
	exec, err := c.executions.Get(ctx, executionID)
	if err != nil {
		return fmt.Errorf("getting execution %s: %w", executionID, ErrNotFound)
	}
	if exec.Status != "waiting_human" {
		return fmt.Errorf("execution %s is not waiting for approval (status: %s): %w", executionID, exec.Status, ErrInvalidState)
	}

	exec, err = c.engine.ApproveHuman(ctx, exec)
	if err != nil {
		return fmt.Errorf("approving human step for execution %s: %w", executionID, err)
	}
	if err := c.executions.Update(ctx, exec); err != nil {
		return fmt.Errorf("persisting execution %s after approval: %w", executionID, err)
	}

	c.resumeExecution(exec)
	return nil
}

// resumeExecution re-launches the execution loop for an approved execution.
func (c *Coordinator) resumeExecution(exec *blueprint.Execution) {
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

// Execute begins blueprint execution for an objective.
// The blueprint is the source of truth — all steps run in order, including
// planner agents and human approval gates. No steps are skipped.
func (c *Coordinator) Execute(ctx context.Context, objectiveID string) error {
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective %s: %w", objectiveID, ErrNotFound)
	}
	if obj.Status != domain.ObjectiveStatusPlanning && obj.Status != domain.ObjectiveStatusApproved {
		return fmt.Errorf("objective %s cannot start execution (status: %s): %w", objectiveID, obj.Status, ErrInvalidState)
	}

	blueprintID := obj.Blueprint
	if blueprintID == "" {
		bp, err := c.engine.ResolveDefaultBlueprint()
		if err != nil {
			return fmt.Errorf("resolving default blueprint for objective %s: %w", objectiveID, err)
		}
		blueprintID = bp.ID
	}

	exec, err := c.engine.Start(ctx, blueprintID, objectiveID)
	if err != nil {
		return fmt.Errorf("starting blueprint execution for objective %s: %w", objectiveID, err)
	}
	if err := c.executions.Create(ctx, exec); err != nil {
		return fmt.Errorf("persisting execution for objective %s: %w", objectiveID, err)
	}

	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusExecuting); err != nil {
		return fmt.Errorf("transitioning objective %s to executing: %w", objectiveID, err)
	}

	if plan, err := c.plans.GetByObjective(ctx, objectiveID); err == nil {
		_ = c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusExecuting)
	}

	c.eventBus.Emit(domain.EventExecutionStarted, objectiveID, "", "",
		"execution_id", exec.ID,
		"blueprint_id", blueprintID,
	)

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
		"blueprint_id", blueprintID,
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

// Kill terminates a live agent process when possible and marks its session failed.
func (c *Coordinator) Kill(ctx context.Context, sessionID string) error {
	return c.tracker.Kill(ctx, sessionID)
}

func truncateForEvent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
