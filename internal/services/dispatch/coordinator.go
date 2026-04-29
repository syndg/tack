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
	"github.com/syndg/tack/internal/harness/preflight"
	"github.com/syndg/tack/internal/observability"
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
	RetryStream(ctx context.Context, streamID string, guidance string) error
	Abort(ctx context.Context, objectiveID string) error
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

type DossierProvider interface {
	GetDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error)
	ExpandDossier(ctx context.Context, objectiveID string, request domain.DossierExpansionRequest) (*domain.Dossier, error)
}

// MailSender sends mail messages (used for escalations via the broker).
type MailSender interface {
	Send(ctx context.Context, msg *domain.MailMessage) error
}

// Config holds all dependencies for constructing a Coordinator.
type Config struct {
	ProjectID     string             // owning project for recovery filtering
	Engine        *blueprint.Engine  // blueprint execution engine
	Scheduler     *Scheduler         // stream scheduling
	Spawner       *Spawner           // agent process spawning
	Discovery     DossierProvider    // dossier retrieval and expansion for planner steps
	AgentModel    string             // default model for non-planner agent steps
	PlannerModel  string             // default model for planner agent steps
	Lifecycle     *lifecycle.Manager // objective state transitions
	MergeEnqueuer MergeEnqueuer      // merge queue integration
	PlanCreator   PlanCreator        // plan creation from planner output
	MailSender    MailSender         // optional: nil disables mail escalation
	Attempts      *db.AttemptStore   // optional: nil disables attempt ledger recording
	Insights      *db.ObjectiveInsightStore
	Executions    *db.ExecutionStore      // execution persistence
	Objectives    *db.ObjectiveStore      // objective persistence
	Plans         *db.PlanStore           // plan persistence
	Streams       *db.StreamStore         // stream persistence
	EventBus      *events.PersistentBus   // event pub/sub
	Observability *observability.Recorder // optional: nil disables canonical operator logging
	Timeouts      config.TimeoutConfig    // per-role timeout configuration
	Preflight     PreflightChecker        // optional: validates runtime requirements before dispatch
	Logger        *slog.Logger            // structured logger
}

type PreflightChecker interface {
	Check(ctx context.Context, blueprintID string) error
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
	discovery     DossierProvider
	lifecycle     *lifecycle.Manager
	mergeEnqueuer MergeEnqueuer
	planCreator   PlanCreator
	mailSender    MailSender
	attempts      *db.AttemptStore
	insights      *db.ObjectiveInsightStore
	executions    *db.ExecutionStore
	objectives    *db.ObjectiveStore
	plans         *db.PlanStore
	streams       *db.StreamStore
	eventBus      *events.PersistentBus
	obs           *observability.Recorder
	tracker       AgentTracker
	logger        *slog.Logger
	preflight     PreflightChecker
	projectID     string
	agentModel    string
	plannerModel  string

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
		discovery:     cfg.Discovery,
		lifecycle:     cfg.Lifecycle,
		mergeEnqueuer: cfg.MergeEnqueuer,
		planCreator:   cfg.PlanCreator,
		mailSender:    cfg.MailSender,
		attempts:      cfg.Attempts,
		insights:      cfg.Insights,
		executions:    cfg.Executions,
		objectives:    cfg.Objectives,
		plans:         cfg.Plans,
		streams:       cfg.Streams,
		eventBus:      cfg.EventBus,
		obs:           cfg.Observability,
		tracker:       newAgentTracker(cfg.Spawner, cfg.Observability, cfg.EventBus, cfg.Timeouts, cfg.Logger),
		logger:        cfg.Logger,
		preflight:     cfg.Preflight,
		projectID:     cfg.ProjectID,
		agentModel:    cfg.AgentModel,
		plannerModel:  cfg.PlannerModel,
		activeExecs:   make(map[string]context.CancelFunc),
	}

	// Register step handlers with the engine.
	cfg.Engine.RegisterHandler(blueprint.StepTypeAgent, c.HandleAgentStep)
	cfg.Engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)

	return c, nil
}

// Start begins recovery processing.
// Execution is triggered exclusively through runs.Start() — the coordinator
// no longer listens for EventObjectiveCreated or merge-completion events.
func (c *Coordinator) Start(ctx context.Context) error {
	c.ctx = ctx
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
	execByID := make(map[string]*blueprint.Execution, len(allExecs))
	for i := range allExecs {
		execByID[allExecs[i].ID] = &allExecs[i]
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
			c.requeueInFlightSubExecutions(ctx, exec, execByID)

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

	for i := range allExecs {
		exec := &allExecs[i]
		if exec.ParentID == "" || exec.Status != "running" || exec.StreamID == "" {
			continue
		}
		parent := execByID[exec.ParentID]
		if parent != nil && parent.Status == "running" {
			continue
		}
		c.resumeStandaloneSubExecution(ctx, exec)
	}
}

func (c *Coordinator) requeueInFlightSubExecutions(ctx context.Context, parent *blueprint.Execution, execByID map[string]*blueprint.Execution) {
	if parent == nil {
		return
	}
	children, err := c.executions.ListByParent(ctx, parent.ID)
	if err != nil {
		c.logger.Warn("execution recovery: failed to list sub-executions", "parent_execution_id", parent.ID, "error", err)
		return
	}
	for i := range children {
		child := &children[i]
		if child.StreamID == "" {
			continue
		}
		if child.Status != "running" && child.Status != "waiting_human" {
			continue
		}
		stream, err := c.streams.Get(ctx, child.StreamID)
		if err != nil {
			c.logger.Warn("execution recovery: failed to load stream for sub-execution reset", "execution_id", child.ID, "stream_id", child.StreamID, "error", err)
			continue
		}
		if stream.ExecutionID != child.ID {
			continue
		}
		if stream.Status == domain.StreamStatusExecuting {
			if err := c.streams.UpdateStatus(ctx, stream.ID, domain.StreamStatusFailed); err == nil {
				if err := c.streams.UpdateStatus(ctx, stream.ID, domain.StreamStatusPending); err != nil {
					c.logger.Warn("execution recovery: failed to requeue stream", "stream_id", stream.ID, "execution_id", child.ID, "error", err)
					continue
				}
			} else {
				c.logger.Warn("execution recovery: failed to fail executing stream before requeue", "stream_id", stream.ID, "execution_id", child.ID, "error", err)
				continue
			}
		}
		c.logger.Info("execution recovery: requeued in-flight stream sub-execution",
			"parent_execution_id", parent.ID,
			"execution_id", child.ID,
			"stream_id", stream.ID,
		)
		if stale, ok := execByID[child.ID]; ok {
			stale.Status = "failed"
		}
	}
}

func (c *Coordinator) resumeStandaloneSubExecution(ctx context.Context, exec *blueprint.Execution) {
	obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		c.logger.Warn("execution recovery: failed to get objective for sub-execution", "execution_id", exec.ID, "objective_id", exec.ObjectiveID, "error", err)
		return
	}
	if obj.Status != domain.ObjectiveStatusExecuting {
		return
	}
	stream, err := c.streams.Get(ctx, exec.StreamID)
	if err != nil {
		c.logger.Warn("execution recovery: failed to get stream for sub-execution", "execution_id", exec.ID, "stream_id", exec.StreamID, "error", err)
		return
	}
	if stream.ExecutionID != exec.ID || stream.Status != domain.StreamStatusExecuting {
		return
	}
	plan, err := c.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		c.logger.Warn("execution recovery: failed to get plan for sub-execution", "execution_id", exec.ID, "objective_id", exec.ObjectiveID, "error", err)
		return
	}
	baseCtx := c.ctx
	if baseCtx == nil {
		baseCtx = ctx
	}
	execCtx, cancel := context.WithCancel(baseCtx)
	retryKey := "retry:" + exec.ID
	c.mu.Lock()
	if existing, ok := c.activeExecs[retryKey]; ok {
		existing()
	}
	c.activeExecs[retryKey] = cancel
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.activeExecs, retryKey)
			c.mu.Unlock()
			cancel()
		}()
		results := make(chan streamResult, 1)
		c.advanceSubExecution(execCtx, exec, stream, plan.ID, results)
		res := <-results
		if res.Error != "" {
			c.logger.Warn("execution recovery: resumed retry sub-execution failed", "stream_id", stream.ID, "execution_id", exec.ID, "error", res.Error)
			return
		}
		c.logger.Info("execution recovery: resumed retry sub-execution completed", "stream_id", stream.ID, "execution_id", exec.ID)
	}()
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

// Abort cancels one objective's active execution and kills its active workers.
func (c *Coordinator) Abort(ctx context.Context, objectiveID string) error {
	retryKeys := map[string]struct{}{}
	if c.executions != nil {
		var (
			execs []blueprint.Execution
			err   error
		)
		if c.projectID != "" {
			execs, err = c.executions.ListByProject(ctx, c.projectID)
		} else {
			execs, err = c.executions.List(ctx)
		}
		if err != nil {
			return fmt.Errorf("listing executions for objective %s: %w", objectiveID, err)
		}
		for _, exec := range execs {
			if exec.ObjectiveID == objectiveID {
				retryKeys["retry:"+exec.ID] = struct{}{}
			}
		}
	}

	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, 1+len(retryKeys))
	for key, cancel := range c.activeExecs {
		if key != objectiveID {
			if _, ok := retryKeys[key]; !ok {
				continue
			}
		}
		cancels = append(cancels, cancel)
		delete(c.activeExecs, key)
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}

	if c.spawner == nil || c.spawner.agentStore == nil {
		return fmt.Errorf("listing agents for objective %s: agent store unavailable", objectiveID)
	}
	sessions, err := c.spawner.agentStore.ListByObjective(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("listing agents for objective %s: %w", objectiveID, err)
	}
	for _, session := range sessions {
		if session.Status != "pending" && session.Status != "running" {
			continue
		}
		if err := c.tracker.Kill(ctx, session.ID); err != nil {
			return fmt.Errorf("killing agent session %s: %w", session.ID, err)
		}
	}
	return nil
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
	if err := c.checkPreflight(ctx, exec.BlueprintID); err != nil {
		return err
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
	if obj.Status == domain.ObjectiveStatusApproved {
		if err := c.checkPreflight(ctx, blueprintID); err != nil {
			return err
		}
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

	if c.obs != nil {
		c.obs.RecordMilestone(observability.Milestone{
			EventType:   domain.EventExecutionStarted,
			ProjectID:   obj.ProjectID,
			ObjectiveID: objectiveID,
			Status:      "started",
			Details: map[string]any{
				"execution_id": exec.ID,
				"blueprint_id": blueprintID,
			},
		})
	}

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

func (c *Coordinator) checkPreflight(ctx context.Context, blueprintID string) error {
	if c.preflight == nil {
		return nil
	}
	if err := c.preflight.Check(ctx, blueprintID); err != nil {
		var failure preflight.Failure
		if errors.As(err, &failure) {
			return err
		}
		return fmt.Errorf("preflight failed: %w", err)
	}
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
			if isExpectedShutdownError(ctx, err) {
				c.logger.Debug("skipping execution persistence during shutdown", "execution_id", exec.ID, "error", err)
				return
			}
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
			if ctx.Err() != nil {
				c.logger.Debug("execution stopped during shutdown", "execution_id", exec.ID, "objective_id", exec.ObjectiveID)
				return
			}
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
