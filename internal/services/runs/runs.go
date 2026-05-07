// Package runs implements run-centric orchestration.
//
// A Run is the durable aggregate that owns objective orchestration from start
// through completion. It hides internal execution details (sub-executions,
// agent sessions, merge queue bookkeeping, scheduler state, merge progression)
// and exposes only semantic operations: start, intervene, inspect, recover.
//
// # Ownership boundary
//
// The runs service owns the construction and lifecycle of all internal
// orchestration services. Callers provide raw infrastructure (stores, engine,
// sandbox provider, etc.) via [Config]; the runs service constructs the full
// orchestration stack internally:
//
//   - Scheduler: dependency-aware stream scheduling and concurrency limiting.
//     Created inside [New] from Config.Streams, Config.Plans, and
//     Config.MaxConcurrent. Never exposed to callers.
//   - Step handlers: deterministic and human blueprint step implementations.
//     Created inside [New] and registered with the blueprint engine.
//   - Spawner: agent process creation, sandbox reuse, credential injection.
//     Created inside [New] from Config.AgentRuntime, Config.SandboxProvider,
//     Config.RulesEngine, Config.ToolCurator, and Config.Credentials.
//   - Coordinator: blueprint execution engine, agent tracker, approval dance.
//     Created inside [New] from the internally-constructed scheduler and
//     spawner plus other Config dependencies.
//   - Merge processor: provided via Config.MergeProcessor; lifecycle (Start/
//     Stop) managed by [Service.Run] and [Service.Stop].
//
// The daemon does not construct or hold references to the scheduler,
// coordinator, or step handlers. All orchestration flows through this boundary:
//
//   - Start: creates a run and delegates to coordinator.Execute
//   - Command(approve): finds blocked execution, delegates to coordinator.Approve
//   - Command(retry): resolves failed stream execution, delegates to coordinator.Retry
//   - Act(abort): cancels one run's active execution and marks it failed
//   - Act(kill_worker): terminates a specific agent session within a run
//   - Snapshot: assembles observable state from persisted records
//   - Run/Stop: manages coordinator and merge processor lifecycle
//
// All orchestration internals are fully owned by this boundary: the
// spawner, scheduler, step handlers, and coordinator are constructed
// inside [New]. The coordinator no longer auto-starts execution from
// events — all executions are created through Start (ensuring every
// objective has a Run record), and all interventions flow through
// Command with no legacy escape hatches. Callers provide only leaf
// infrastructure via [Config].
//
// # Recovery (issue #26)
//
// Run() reconciles in-flight runs on daemon restart. Active/blocked runs
// are checked against their objective's current state: terminal objectives
// sync immediately, waiting_human objectives mark the run blocked, and
// executing objectives are left active (the coordinator resumes them).
// Orphaned runs (objective missing) are marked failed.
//
// # Retry flow (issue #25)
//
// Snapshot exposes retryable failure information: failed streams with an
// associated execution are marked Retryable=true and include the last
// step error from the failed execution. Callers inspect the snapshot to
// identify which streams can be retried and why they failed.
// Command(retry) routes through the run boundary with guidance, resolves
// the failed execution from the stream, and delegates to the coordinator.
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/harness/preflight"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// ErrInvalidState is returned when an operation is invalid for the current state.
var ErrInvalidState = errors.New("invalid state")

// RunView is the operator-facing view returned by the runtime boundary.
type RunView = domain.Snapshot

// Runs is the run-centric orchestration boundary.
//
// Ensure creates or resumes a run for an objective and returns the run view.
// Start creates a new run for an objective and begins execution.
// Act sends an intervention (approve, retry, abort, kill_worker) to a run.
// Command is the legacy name for Act and remains available for compatibility.
// Snapshot returns the observable state of a run at a point in time.
// Recover reconciles durable run state after process restart.
// Run starts the background orchestration loop (coordinator, merge processor,
// run status synchronization, and recovery/reconciliation).
type Runs interface {
	Ensure(ctx context.Context, objectiveID string) (RunView, error)
	Act(ctx context.Context, runID string, action domain.Command) (RunView, error)
	Start(ctx context.Context, objectiveID string) (domain.Snapshot, error)
	Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error)
	Snapshot(ctx context.Context, runID string) (domain.Snapshot, error)
	Recover(ctx context.Context) error
	Run(ctx context.Context) error
	Stop()
}

// RunRuntime is the caller-first runtime seam for objective execution.
type RunRuntime interface {
	Ensure(ctx context.Context, objectiveID string) (RunView, error)
	Act(ctx context.Context, runID string, action domain.Command) (RunView, error)
	View(ctx context.Context, runID string) (RunView, error)
	Recover(ctx context.Context) error
}

type DossierEnsurer interface {
	EnsureDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error)
	GetDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error)
	ExpandDossier(ctx context.Context, objectiveID string, request domain.DossierExpansionRequest) (*domain.Dossier, error)
}

// MergeOrchestrator is the merge-processor surface needed by the runs
// boundary. It combines lifecycle (Start/Stop), merge-queue enqueue, and
// merge-entry reset into a single interface. merge.Processor satisfies this.
type MergeOrchestrator interface {
	Start(ctx context.Context) error
	Stop()
	// MergeEnqueuer methods — used by coordinator step handlers.
	EnqueueStream(ctx context.Context, streamID string) error
	MergerSandboxID(objectiveID string) string
	// MergeHelper method — used by step handlers for restart recovery.
	ResetMergingEntries(ctx context.Context, streamID string)
}

// Config bundles all dependencies needed to construct the runs orchestration
// stack. The runs service owns construction of internal helpers (scheduler,
// step handlers, coordinator) — callers provide raw infrastructure only.
type Config struct {
	ProjectID   string
	ProjectRoot string
	Discovery   DossierEnsurer

	// Orchestrator overrides internal coordinator construction (testing only).
	// When non-nil, the service uses this orchestrator directly and ignores
	// the fields below marked "construction-only". When nil, the service
	// constructs the full orchestration stack internally.
	Orchestrator dispatch.Orchestrator

	// --- Construction-only fields (ignored when Orchestrator is set) ---

	// Engine is the blueprint execution engine. The runs service registers
	// step handlers with it and passes it to the internally-constructed
	// coordinator.
	Engine *blueprint.Engine

	// Lifecycle manages objective state transitions.
	Lifecycle *lifecycle.Manager

	// PlanCreator creates plans from planner agent output.
	PlanCreator dispatch.PlanCreator

	// MailSender sends mail messages (escalations). Optional: nil disables.
	MailSender dispatch.MailSender

	// GateRunner runs quality gates in sandboxes.
	GateRunner *gates.Runner

	// SandboxProvider creates and manages sandboxes.
	SandboxProvider sandbox.SandboxProvider

	// AgentRuntime runs agent processes inside sandboxes.
	AgentRuntime runtime.AgentRuntime

	// RulesEngine evaluates project rules for agent overlays.
	RulesEngine *rules.Engine

	// ToolCurator curates per-role tool lists for agents.
	ToolCurator *tools.Curator

	// Credentials provides API keys and tokens for agent injection.
	Credentials *credentials.Store

	RuntimeAuth         config.RuntimeAuthConfig
	SandboxProviderName string
	DaemonExternalURL   string
	AgentModel          string
	PlannerModel        string
	DeterministicModel  string

	// DaemonURL is the URL agents use to call back to the daemon.
	DaemonURL string
	// DaemonToken authenticates agent and CLI calls to the daemon.
	DaemonToken string

	// Observability records canonical operator-facing timeline data. Optional: nil disables.
	Observability *observability.Recorder

	// Timeouts configures per-role agent timeout behavior.
	Timeouts config.TimeoutConfig

	// MaxConcurrent caps the number of concurrently executing streams.
	MaxConcurrent int

	// BaseBranch is the git base branch for merge operations.
	BaseBranch string

	// Git identity defaults for sandboxed commits.
	GitAuthorName  string
	GitAuthorEmail string

	// --- Always-required fields ---

	// MergeProcessor owns the merge queue lifecycle and merge operations.
	// Must satisfy MergeOrchestrator. When Orchestrator is set (testing),
	// only Start/Stop are used.
	MergeProcessor MergeOrchestrator
	Ledger         RunLedger

	Runs       *db.RunStore
	Attempts   *db.AttemptStore
	Insights   *db.ObjectiveInsightStore
	Objectives *db.ObjectiveStore
	Plans      *db.PlanStore
	Streams    *db.StreamStore
	Executions *db.ExecutionStore
	Agents     *db.AgentStore

	EventBus *events.PersistentBus
	Logger   *slog.Logger
}

// Service implements the Runs boundary.
type Service struct {
	ledger RunLedger

	runs       *db.RunStore
	attempts   *db.AttemptStore
	insights   *db.ObjectiveInsightStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	executions *db.ExecutionStore
	agents     *db.AgentStore

	// Internal orchestration services — owned by runs, not exposed to callers.
	// The scheduler, step handlers, and agent tracker are further internal to
	// the coordinator and never escape the runs boundary.
	coordinator    dispatch.Orchestrator
	mergeProcessor MergeOrchestrator
	lifecycle      *lifecycle.Manager
	sandboxProv    sandbox.SandboxProvider
	eventBus       *events.PersistentBus
	projectID      string
	discovery      DossierEnsurer

	runLocksMu sync.Mutex
	runLocks   map[string]*sync.Mutex

	logger *slog.Logger
}

// New constructs a runs Service from the provided Config.
//
// When cfg.Orchestrator is nil (production path), New constructs the full
// internal orchestration stack: scheduler, step handlers, and coordinator.
// The scheduler is an implementation detail of this boundary — callers never
// see or interact with it. When cfg.Orchestrator is non-nil (test path),
// the provided mock is used directly and construction-only fields are ignored.
func New(cfg Config) (*Service, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "runs")

	coordinator := cfg.Orchestrator
	if coordinator == nil {
		// Construct the internal orchestration stack.
		var missing []string
		if cfg.Engine == nil {
			missing = append(missing, "Engine")
		}
		if cfg.AgentRuntime == nil {
			missing = append(missing, "AgentRuntime")
		}
		if cfg.Lifecycle == nil {
			missing = append(missing, "Lifecycle")
		}
		if cfg.PlanCreator == nil {
			missing = append(missing, "PlanCreator")
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("runs.Config: missing required fields for coordinator construction: %s", strings.Join(missing, ", "))
		}

		// Spawner: agent process creation, sandbox reuse, credential injection.
		// Created here as an internal implementation detail of the runs boundary.
		spawner := dispatch.NewSpawner(
			cfg.Agents, cfg.AgentRuntime, cfg.SandboxProvider,
			cfg.RulesEngine, cfg.ToolCurator, cfg.EventBus, cfg.Observability,
			cfg.Credentials, cfg.RuntimeAuth, logger, cfg.DaemonURL,
			cfg.GitAuthorName, cfg.GitAuthorEmail, cfg.DaemonToken,
		)

		// Scheduler: stream dependency resolution and concurrency limiting.
		// Created here as an internal implementation detail of the runs boundary.
		maxConcurrent := cfg.MaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = 5
		}
		scheduler := dispatch.NewScheduler(cfg.Streams, cfg.Plans, maxConcurrent, cfg.EventBus, logger)

		// Step handlers for deterministic and human blueprint steps.
		handlers := dispatch.NewHandlers(
			scheduler, cfg.GateRunner, cfg.Lifecycle, cfg.MergeProcessor, cfg.Engine,
			cfg.AgentRuntime,
			cfg.Plans, cfg.Streams, cfg.Objectives, cfg.Executions, cfg.Agents, cfg.Attempts,
			cfg.SandboxProvider, cfg.EventBus, cfg.Observability, cfg.BaseBranch, cfg.Credentials, cfg.RuntimeAuth, cfg.DeterministicModel, logger,
		)
		cfg.Engine.RegisterHandler(blueprint.StepTypeDeterministic, handlers.HandleDeterministic)
		cfg.Engine.RegisterHandler(blueprint.StepTypeHuman, handlers.HandleHuman)

		// Coordinator: drives blueprint execution, owns agent tracker.
		// Agent and blueprint_ref step handlers are registered inside NewCoordinator.
		preflightChecker := preflight.New(preflight.Options{
			Blueprints:               cfg.Engine,
			ProjectRoot:              cfg.ProjectRoot,
			Credentials:              cfg.Credentials,
			RuntimeAuthMode:          cfg.RuntimeAuth.Mode,
			RuntimeAuthProvider:      cfg.RuntimeAuth.Provider,
			RuntimeAuthMethod:        cfg.RuntimeAuth.Method,
			RuntimeAuthCredentialRef: cfg.RuntimeAuth.CredentialRef,
			SandboxProvider:          cfg.SandboxProviderName,
			DaemonExternalURL:        cfg.DaemonExternalURL,
		})
		var err error
		coordinator, err = dispatch.NewCoordinator(dispatch.Config{
			ProjectID:     cfg.ProjectID,
			Engine:        cfg.Engine,
			Scheduler:     scheduler,
			Spawner:       spawner,
			Discovery:     cfg.Discovery,
			AgentModel:    cfg.AgentModel,
			PlannerModel:  cfg.PlannerModel,
			Lifecycle:     cfg.Lifecycle,
			MergeEnqueuer: cfg.MergeProcessor,
			PlanCreator:   cfg.PlanCreator,
			MailSender:    cfg.MailSender,
			Attempts:      cfg.Attempts,
			Insights:      cfg.Insights,
			Executions:    cfg.Executions,
			Objectives:    cfg.Objectives,
			Plans:         cfg.Plans,
			Streams:       cfg.Streams,
			EventBus:      cfg.EventBus,
			Observability: cfg.Observability,
			Timeouts:      cfg.Timeouts,
			Preflight:     preflightChecker,
			Logger:        logger,
		})
		if err != nil {
			return nil, fmt.Errorf("constructing coordinator: %w", err)
		}
	}

	ledger := cfg.Ledger
	if ledger == nil {
		ledger = &storeRunLedger{
			runs:       cfg.Runs,
			attempts:   cfg.Attempts,
			objectives: cfg.Objectives,
			plans:      cfg.Plans,
			streams:    cfg.Streams,
			executions: cfg.Executions,
			agents:     cfg.Agents,
		}
	}

	return &Service{
		ledger:         ledger,
		runs:           cfg.Runs,
		attempts:       cfg.Attempts,
		insights:       cfg.Insights,
		objectives:     cfg.Objectives,
		plans:          cfg.Plans,
		streams:        cfg.Streams,
		executions:     cfg.Executions,
		agents:         cfg.Agents,
		coordinator:    coordinator,
		mergeProcessor: cfg.MergeProcessor,
		lifecycle:      cfg.Lifecycle,
		sandboxProv:    cfg.SandboxProvider,
		eventBus:       cfg.EventBus,
		projectID:      cfg.ProjectID,
		discovery:      cfg.Discovery,
		runLocks:       make(map[string]*sync.Mutex),
		logger:         logger,
	}, nil
}

// Ensure creates or resumes a run for an objective and returns the current
// operator-facing run view. Existing runs are not duplicated.
func (s *Service) Ensure(ctx context.Context, objectiveID string) (RunView, error) {
	if run, err := s.ledger.GetRunByObjective(ctx, objectiveID); err == nil {
		return s.View(ctx, run.ID)
	}
	return s.startNew(ctx, objectiveID)
}

// Start creates a new run for an objective and begins execution.
// It validates the objective is in a startable state, creates a durable
// Run record, delegates execution to the coordinator, and returns a
// snapshot reflecting the run's initial state.
func (s *Service) Start(ctx context.Context, objectiveID string) (domain.Snapshot, error) {
	return s.startNew(ctx, objectiveID)
}

func (s *Service) startNew(ctx context.Context, objectiveID string) (RunView, error) {
	obj, err := s.ledger.GetObjective(ctx, objectiveID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("getting objective: %w", err)
	}

	if obj.Status != domain.ObjectiveStatusPlanning &&
		obj.Status != domain.ObjectiveStatusApproved {
		return domain.Snapshot{}, fmt.Errorf(
			"objective %s cannot start execution (status: %s): %w",
			objectiveID, obj.Status, ErrInvalidState,
		)
	}
	if s.discovery != nil {
		if _, err := s.discovery.EnsureDossier(ctx, objectiveID); err != nil {
			return domain.Snapshot{}, fmt.Errorf("ensuring dossier for objective %s: %w", objectiveID, err)
		}
	}

	run := &domain.Run{ObjectiveID: objectiveID}
	if err := s.ledger.CreateRun(ctx, run); err != nil {
		return domain.Snapshot{}, fmt.Errorf("creating run: %w", err)
	}

	s.logger.Info("starting run", "run_id", run.ID, "objective_id", objectiveID)

	if err := s.coordinator.Execute(ctx, objectiveID); err != nil {
		// Mark run as failed since execution couldn't start.
		_ = s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusFailed)
		return domain.Snapshot{}, fmt.Errorf("starting execution: %w", err)
	}

	snap, err := s.Snapshot(ctx, run.ID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("building initial snapshot: %w", err)
	}

	return snap, nil
}

// Act sends an intervention through the runtime boundary.
func (s *Service) Act(ctx context.Context, runID string, action domain.Command) (RunView, error) {
	return s.Command(ctx, runID, action)
}

// Command sends an intervention (approve, retry, abort, kill_worker) to a run.
//
// This is the single entry point for all run interventions. It owns the
// choreography that was previously spread across coordinator.Approve,
// coordinator.Retry, and coordinator.Kill — callers no longer need to
// understand execution IDs, session IDs, or internal state machines.
//
// The coordinator remains the execution engine, but Command owns the
// run-level state transitions and delegates internally.
func (s *Service) Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error) {
	unlock := s.lockRun(runID)
	defer unlock()

	run, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading run: %w", err)
	}

	switch cmd.Kind {
	case domain.CommandApprove:
		return s.commandApprove(ctx, run)
	case domain.CommandRetry:
		return s.commandRetry(ctx, run, cmd)
	case domain.CommandAbort:
		return s.commandAbort(ctx, run, cmd)
	case domain.CommandKill, domain.CommandKillWorker:
		return s.commandKill(ctx, run, cmd)
	default:
		return domain.Snapshot{}, fmt.Errorf("unknown command kind %q: %w", cmd.Kind, ErrInvalidState)
	}
}

func (s *Service) lockRun(runID string) func() {
	s.runLocksMu.Lock()
	lock := s.runLocks[runID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.runLocks[runID] = lock
	}
	s.runLocksMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

// commandApprove handles the approve intervention.
// Finds the blocked execution for the run's objective and delegates to
// coordinator.Approve, then updates run status from blocked → active.
func (s *Service) commandApprove(ctx context.Context, run *domain.Run) (domain.Snapshot, error) {
	// Plan approval and other human-gate interventions can race slightly with
	// the execution loop persisting the waiting_human state. Wait briefly for
	// the top-level execution to reach the blocked state before approving.
	exec, err := s.waitForExecutionWaitingHuman(ctx, run.ObjectiveID, 5*time.Second)
	if err != nil {
		return domain.Snapshot{}, err
	}

	if err := s.coordinator.Approve(ctx, exec.ID); err != nil {
		return domain.Snapshot{}, fmt.Errorf("approving execution: %w", err)
	}

	// Transition run back to active now that it's unblocked.
	if run.Status == domain.RunStatusBlocked {
		if err := s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusActive); err != nil {
			s.logger.Warn("failed to update run status after approve",
				"run_id", run.ID, "error", err)
		}
	}

	s.logger.Info("run approved", "run_id", run.ID, "execution_id", exec.ID)
	return s.Snapshot(ctx, run.ID)
}

func (s *Service) waitForExecutionWaitingHuman(ctx context.Context, objectiveID string, timeout time.Duration) (*blueprint.Execution, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := "missing"

	for {
		exec, err := s.ledger.GetExecutionByObjective(ctx, objectiveID)
		if err == nil {
			lastStatus = exec.Status
			switch exec.Status {
			case "waiting_human":
				return exec, nil
			case "completed", "failed":
				return nil, fmt.Errorf(
					"run for objective %s has no execution waiting for approval (status: %s): %w",
					objectiveID, exec.Status, ErrInvalidState,
				)
			}
		}

		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for execution approval state: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"run for objective %s has no execution waiting for approval (status: %s): %w",
				objectiveID, lastStatus, ErrInvalidState,
			)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for execution approval state: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// commandRetry handles the retry intervention for a failed stream.
// Resolves the failed sub-execution from the stream, delegates to
// coordinator.Retry, and ensures the run stays in active status.
func (s *Service) commandRetry(ctx context.Context, run *domain.Run, cmd domain.Command) (domain.Snapshot, error) {
	if cmd.StreamID == "" {
		return domain.Snapshot{}, fmt.Errorf("retry command requires stream_id: %w", ErrInvalidState)
	}

	// Find the failed sub-execution for this stream.
	stream, err := s.ledger.GetStream(ctx, cmd.StreamID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading stream %s: %w", cmd.StreamID, err)
	}
	if stream.Status != "failed" {
		return domain.Snapshot{}, fmt.Errorf(
			"stream %s is not failed (status: %s): %w",
			cmd.StreamID, stream.Status, ErrInvalidState,
		)
	}
	if len(cmd.ScopeAdditions) > 0 {
		mergedScope := mergeFileScope(stream.FileScope, cmd.ScopeAdditions)
		if err := s.ledger.UpdateStreamFileScope(ctx, stream.ID, mergedScope); err != nil {
			return domain.Snapshot{}, fmt.Errorf("updating stream retry scope: %w", err)
		}
		stream.FileScope = mergedScope
	}

	var retryErr error
	if stream.ExecutionID == "" {
		retryErr = s.coordinator.RetryStream(ctx, stream.ID, cmd.Guidance)
	} else {
		retryErr = s.coordinator.Retry(ctx, stream.ExecutionID, cmd.Guidance)
	}
	if retryErr != nil {
		return domain.Snapshot{}, fmt.Errorf("retrying stream execution: %w", retryErr)
	}
	if strings.TrimSpace(cmd.Guidance) != "" && s.insights != nil {
		if err := s.insights.Create(ctx, &domain.ObjectiveInsight{ProjectID: run.ProjectID, ObjectiveID: run.ObjectiveID, StreamID: cmd.StreamID, ExecutionID: stream.ExecutionID, Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance, Summary: cmd.Guidance, Detail: cmd.Guidance, Payload: map[string]string{"command": string(cmd.Kind)}}); err != nil {
			s.logger.Warn("recording retry guidance insight", "run_id", run.ID, "stream_id", cmd.StreamID, "error", err)
		}
	}

	// Ensure run is marked active (it may be in partial/failed state from
	// the original execution completing with failures).
	if run.Status != domain.RunStatusActive {
		if err := s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusActive); err != nil {
			s.logger.Warn("failed to update run status after retry",
				"run_id", run.ID, "error", err)
		}
	}

	s.logger.Info("run stream retry initiated",
		"run_id", run.ID, "stream_id", cmd.StreamID, "has_guidance", cmd.Guidance != "")
	return s.Snapshot(ctx, run.ID)
}

func mergeFileScope(existing, additions []string) []string {
	merged := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(merged)+len(additions))
	for _, item := range merged {
		item = strings.TrimSpace(item)
		if item != "" {
			seen[item] = struct{}{}
		}
	}
	for _, item := range additions {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

// commandAbort handles the abort intervention.
// Kills all active agents for the run and marks it as failed.
func (s *Service) commandAbort(ctx context.Context, run *domain.Run, cmd domain.Command) (domain.Snapshot, error) {
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed {
		return domain.Snapshot{}, fmt.Errorf(
			"run %s is already terminal (status: %s): %w",
			run.ID, run.Status, ErrInvalidState,
		)
	}

	if err := s.coordinator.Abort(ctx, run.ObjectiveID); err != nil {
		return domain.Snapshot{}, fmt.Errorf("aborting run workers: %w", err)
	}

	if err := s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusFailed); err != nil {
		return domain.Snapshot{}, fmt.Errorf("marking run failed: %w", err)
	}

	reason := cmd.Reason
	if reason == "" {
		reason = "aborted by user"
	}
	s.logger.Info("run aborted", "run_id", run.ID, "reason", reason)
	return s.Snapshot(ctx, run.ID)
}

// commandKill terminates a specific agent session within a run.
// Unlike abort (which stops the entire run), kill targets a single agent
// and leaves the run active so other streams can continue.
func (s *Service) commandKill(ctx context.Context, run *domain.Run, cmd domain.Command) (domain.Snapshot, error) {
	if cmd.SessionID == "" {
		return domain.Snapshot{}, fmt.Errorf("kill command requires session_id: %w", ErrInvalidState)
	}

	// Verify the agent session belongs to this run's objective.
	session, err := s.ledger.GetAgentSession(ctx, cmd.SessionID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading agent session %s: %w", cmd.SessionID, err)
	}
	if session.ObjectiveID != run.ObjectiveID {
		return domain.Snapshot{}, fmt.Errorf(
			"agent session %s belongs to objective %s, not run %s (objective %s): %w",
			cmd.SessionID, session.ObjectiveID, run.ID, run.ObjectiveID, ErrInvalidState,
		)
	}

	if err := s.coordinator.Kill(ctx, cmd.SessionID); err != nil {
		return domain.Snapshot{}, fmt.Errorf("killing agent session: %w", err)
	}

	s.logger.Info("agent killed via run",
		"run_id", run.ID, "session_id", cmd.SessionID)
	return s.Snapshot(ctx, run.ID)
}

// Snapshot returns the observable state of a run by assembling current
// persisted state from the run record, objective, plan, streams, and
// execution.
func (s *Service) Snapshot(ctx context.Context, runID string) (domain.Snapshot, error) {
	run, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading run: %w", err)
	}

	snap := domain.Snapshot{
		RunID:       run.ID,
		ProjectID:   run.ProjectID,
		ObjectiveID: run.ObjectiveID,
		Status:      run.Status,
		CreatedAt:   run.CreatedAt,
		UpdatedAt:   run.UpdatedAt,
	}

	// Populate terminal outcome.
	switch run.Status {
	case domain.RunStatusCompleted, domain.RunStatusPartial, domain.RunStatusFailed:
		snap.Outcome = &domain.Outcome{Status: run.Status}
	}

	// Populate blocked state if run is blocked.
	if run.Status == domain.RunStatusBlocked {
		snap.Blocked = s.resolveBlocked(ctx, run)
	}

	// Populate stream states from the objective's plan.
	snap.Streams = s.resolveStreams(ctx, run.ObjectiveID)
	snap.Workers = s.resolveWorkers(ctx, run.ObjectiveID)

	return snap, nil
}

func (s *Service) resolveWorkers(ctx context.Context, objectiveID string) []domain.RunWorkerState {
	sessions, err := s.ledger.ListAgentSessionsByObjective(ctx, objectiveID)
	if err != nil {
		s.logger.Warn("failed to resolve run workers", "objective_id", objectiveID, "error", err)
		return nil
	}
	workers := make([]domain.RunWorkerState, 0, len(sessions))
	for _, session := range sessions {
		if session.Status != "pending" && session.Status != "running" {
			continue
		}
		workers = append(workers, domain.RunWorkerState{
			SessionID: session.ID,
			StreamID:  session.StreamID,
			Role:      session.Role,
			Status:    session.Status,
			SandboxID: session.SandboxID,
		})
	}
	return workers
}

// View returns the operator-facing state of a run.
func (s *Service) View(ctx context.Context, runID string) (RunView, error) {
	return s.Snapshot(ctx, runID)
}

// SnapshotByObjective returns a snapshot for the most recent run of an objective.
func (s *Service) SnapshotByObjective(ctx context.Context, objectiveID string) (domain.Snapshot, error) {
	run, err := s.ledger.GetRunByObjective(ctx, objectiveID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading run for objective: %w", err)
	}
	return s.Snapshot(ctx, run.ID)
}

// Run starts the background orchestration loop.
//
// This is the single entry point for all background orchestration work.
// It owns the lifecycle of internal services (coordinator, merge processor)
// and keeps Run records synchronized with objective state changes.
//
// On startup, Run reconciles in-flight runs that were active or blocked when
// the daemon last shut down. It checks each run's objective state and updates
// the run record accordingly — terminal objectives sync immediately, executing
// objectives are resumed by the coordinator's own recovery, and waiting_human
// objectives are marked blocked so snapshots reflect the correct gate state.
//
// The daemon should call Run(ctx) once at startup and Stop() at shutdown.
// Callers no longer need to manage coordinator or merge processor directly.
func (s *Service) Run(ctx context.Context) error {
	// Start internal orchestration services.
	// The coordinator's Start() recovers in-flight executions (resumes
	// runExecution goroutines for running top-level executions).
	if err := s.coordinator.Start(ctx); err != nil {
		return fmt.Errorf("starting coordinator: %w", err)
	}
	if s.mergeProcessor != nil {
		if err := s.mergeProcessor.Start(ctx); err != nil {
			return fmt.Errorf("starting merge processor: %w", err)
		}
	}

	// Reconcile run records that were in-flight before the daemon restarted.
	// This must happen after coordinator.Start() so that execution recovery
	// has already claimed running executions.
	if err := s.Recover(ctx); err != nil {
		return err
	}

	// Subscribe to objective lifecycle events to keep Run records in sync.
	// When the coordinator transitions an objective (completed, partial, failed,
	// blocked), the corresponding Run record is updated automatically.
	sub, unsub := s.eventBus.Subscribe(128)
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
				if event.Type == domain.EventObjectiveUpdated && event.Objective != "" {
					s.syncRunStatus(ctx, event)
					continue
				}
				if event.Type == domain.EventMergeCompleted && event.Objective != "" {
					s.completeMerge(ctx, event.Objective)
					continue
				}
				if (event.Type == domain.EventRecoveryBlocked || event.Type == domain.EventRecoveryResumed) && event.Objective != "" {
					s.syncRunRecoveryStatus(ctx, event)
				}
			}
		}
	}()

	s.logger.Info("runs orchestration loop started")
	return nil
}

// Recover reconciles persisted run state after restart without starting or
// stopping background workers. Run calls this after internal services start;
// tests and future startup callers can exercise recovery through the runtime
// boundary directly.
func (s *Service) Recover(ctx context.Context) error {
	s.recoverRuns(ctx)
	s.recoverMergeCompletions(ctx)
	return nil
}

// recoverRuns reconciles run records that were active or blocked when the
// daemon last shut down. For each in-flight run, it checks the objective's
// current state and updates the run record:
//
//   - Terminal objective (completed/partial/failed) → sync run status
//   - Waiting for human approval → mark run blocked
//   - Still executing → leave as active (coordinator resumes the execution)
//   - Objective missing or in unexpected state → mark run failed
func (s *Service) recoverRuns(ctx context.Context) {
	var (
		activeRuns []domain.Run
		err        error
	)
	activeRuns, err = s.ledger.ListActiveRuns(ctx, s.projectID)
	if err != nil {
		s.logger.Warn("failed to list active runs for recovery", "error", err)
		return
	}

	if len(activeRuns) == 0 {
		return
	}

	s.logger.Info("recovering in-flight runs", "count", len(activeRuns))

	for _, run := range activeRuns {
		obj, err := s.ledger.GetObjective(ctx, run.ObjectiveID)
		if err != nil {
			// Objective missing — mark run as failed so it doesn't stay orphaned.
			s.logger.Warn("objective not found during run recovery, marking run failed",
				"run_id", run.ID, "objective_id", run.ObjectiveID, "error", err)
			_ = s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusFailed)
			continue
		}

		var newStatus domain.RunStatus
		switch obj.Status {
		case domain.ObjectiveStatusCompleted:
			newStatus = domain.RunStatusCompleted
		case domain.ObjectiveStatusPartial:
			if s.hasActiveRecoveryBlock(ctx, run.ObjectiveID) {
				newStatus = domain.RunStatusBlocked
			} else {
				newStatus = domain.RunStatusPartial
			}
		case domain.ObjectiveStatusFailed:
			newStatus = domain.RunStatusFailed
		case domain.ObjectiveStatusExecuting:
			if s.hasActiveRecoveryBlock(ctx, run.ObjectiveID) {
				newStatus = domain.RunStatusBlocked
				break
			}
			// Coordinator's recoverExecutions handles resuming the execution.
			// Check if there's a waiting_human execution that should block the run.
			if exec, err := s.ledger.GetExecutionByObjective(ctx, run.ObjectiveID); err == nil {
				if exec.Status == "waiting_human" {
					newStatus = domain.RunStatusBlocked
				}
			}
			// Otherwise leave as active — coordinator is driving the execution.
		default:
			// Objective in unexpected pre-execution state (planning, approved)
			// but run was active — mark failed since execution is no longer running.
			s.logger.Warn("objective in unexpected state during run recovery",
				"run_id", run.ID, "objective_id", run.ObjectiveID, "status", obj.Status)
			newStatus = domain.RunStatusFailed
		}

		if newStatus == "" || newStatus == run.Status {
			continue
		}

		if err := s.ledger.UpdateRunStatus(ctx, run.ID, newStatus); err != nil {
			s.logger.Warn("failed to update run during recovery",
				"run_id", run.ID, "target_status", newStatus, "error", err)
			continue
		}

		s.logger.Info("recovered run",
			"run_id", run.ID, "objective_id", run.ObjectiveID,
			"old_status", run.Status, "new_status", newStatus)
	}
}

func (s *Service) recoverMergeCompletions(ctx context.Context) {
	objectives, err := s.listPartialObjectives(ctx)
	if err != nil {
		s.logger.Warn("failed to list partial objectives for merge recovery", "error", err)
		return
	}
	for _, obj := range objectives {
		s.completeMerge(ctx, obj.ID)
	}
}

func (s *Service) listPartialObjectives(ctx context.Context) ([]domain.Objective, error) {
	if s.objectives == nil {
		return nil, nil
	}
	var objectives []domain.Objective
	var err error
	if s.projectID != "" {
		objectives, err = s.objectives.ListByProject(ctx, s.projectID)
	} else {
		objectives, err = s.objectives.List(ctx)
	}
	if err != nil {
		return nil, err
	}
	partials := make([]domain.Objective, 0, len(objectives))
	for _, obj := range objectives {
		if obj.Status == domain.ObjectiveStatusPartial {
			partials = append(partials, obj)
		}
	}
	return partials, nil
}

func (s *Service) completeMerge(ctx context.Context, objectiveID string) {
	run, err := s.ledger.GetRunByObjective(ctx, objectiveID)
	if err != nil {
		return
	}
	unlock := s.lockRun(run.ID)
	defer unlock()

	obj, err := s.ledger.GetObjective(ctx, objectiveID)
	if err != nil || obj.Status != domain.ObjectiveStatusPartial {
		return
	}
	if !s.allStreamsMerged(ctx, objectiveID) {
		return
	}
	if !s.rePushMergerBranch(ctx, objectiveID) {
		s.logger.Warn("staying partial; merger branch re-push failed", "objective_id", objectiveID)
		return
	}

	if s.lifecycle != nil {
		if !lifecycle.IsValidTransition(obj.Status, domain.ObjectiveStatusCompleted) {
			return
		}
		if err := s.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusCompleted); err != nil {
			s.logger.Warn("failed to complete partial objective after merge", "objective_id", objectiveID, "error", err)
			return
		}
	} else if s.objectives != nil {
		if err := s.objectives.UpdateStatus(ctx, objectiveID, domain.ObjectiveStatusCompleted); err != nil {
			s.logger.Warn("failed to complete partial objective after merge", "objective_id", objectiveID, "error", err)
			return
		}
	}

	if run.Status != domain.RunStatusCompleted {
		if err := s.ledger.UpdateRunStatus(ctx, run.ID, domain.RunStatusCompleted); err != nil {
			s.logger.Warn("failed to complete run after merge", "run_id", run.ID, "objective_id", objectiveID, "error", err)
			return
		}
	}
	s.logger.Info("run completed after merge", "run_id", run.ID, "objective_id", objectiveID)
}

func (s *Service) allStreamsMerged(ctx context.Context, objectiveID string) bool {
	plan, err := s.ledger.GetPlanByObjective(ctx, objectiveID)
	if err != nil {
		return false
	}
	streams, err := s.ledger.ListStreamsByPlan(ctx, plan.ID)
	if err != nil || len(streams) == 0 {
		return false
	}
	for _, stream := range streams {
		if stream.Status != domain.StreamStatusMerged {
			return false
		}
	}
	return true
}

func (s *Service) rePushMergerBranch(ctx context.Context, objectiveID string) bool {
	if s.mergeProcessor == nil || s.sandboxProv == nil {
		return true
	}
	mergerID := s.mergeProcessor.MergerSandboxID(objectiveID)
	if mergerID == "" {
		return true
	}
	sb, err := s.sandboxProv.Get(ctx, mergerID)
	if err != nil {
		s.logger.Warn("failed to get merger sandbox", "sandbox_id", mergerID, "error", err)
		return false
	}
	remoteCheck, err := sb.Exec(ctx, "git remote get-url origin", sandbox.ExecOpts{})
	if err != nil || remoteCheck.ExitCode != 0 {
		return true
	}
	branchResult, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil || branchResult.ExitCode != 0 {
		s.logger.Warn("failed to get merger branch name", "objective_id", objectiveID)
		return false
	}
	branch := strings.TrimSpace(branchResult.Stdout)
	pushResult, err := sb.Exec(ctx, fmt.Sprintf("git push -f origin %s", naming.ShellQuote(branch)), sandbox.ExecOpts{})
	if err != nil || pushResult.ExitCode != 0 {
		s.logger.Warn("failed to re-push merger branch after merge completion", "objective_id", objectiveID, "branch", branch, "error", pushResult.Stderr)
		return false
	}
	return true
}

// Stop shuts down all internal orchestration services.
// Stops the merge processor first (no new merges), then the coordinator
// (cancels executions, kills agents).
func (s *Service) Stop() {
	if s.mergeProcessor != nil {
		s.mergeProcessor.Stop()
	}
	s.coordinator.Stop()
	s.logger.Info("runs orchestration stopped")
}

// syncRunStatus maps an objective status change to the corresponding Run
// status and updates the Run record. This keeps Run records accurate without
// requiring the coordinator to know about runs.
func (s *Service) syncRunStatus(ctx context.Context, event domain.Event) {
	// Parse the "to" status from the event payload.
	var payload map[string]string
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return
	}
	toStatus := domain.ObjectiveStatus(payload["to"])
	if toStatus == "" {
		return
	}

	// Map objective status → run status.
	var runStatus domain.RunStatus
	switch toStatus {
	case domain.ObjectiveStatusCompleted:
		runStatus = domain.RunStatusCompleted
	case domain.ObjectiveStatusPartial:
		if s.hasActiveRecoveryBlock(ctx, event.Objective) {
			runStatus = domain.RunStatusBlocked
		} else {
			runStatus = domain.RunStatusPartial
		}
	case domain.ObjectiveStatusFailed:
		runStatus = domain.RunStatusFailed
	default:
		// Non-terminal transitions (planning, approved, executing) don't
		// change the run status — the run stays active.
		return
	}

	run, err := s.ledger.GetRunByObjective(ctx, event.Objective)
	if err != nil {
		// No run for this objective — objective may predate run-centric API.
		return
	}

	// Don't downgrade terminal runs (e.g. if objective is retried after failure,
	// a new run should be created rather than reusing the old one).
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusPartial {
		if runStatus == domain.RunStatusFailed {
			return
		}
	}

	if run.Status == runStatus {
		return // already in sync
	}

	if err := s.ledger.UpdateRunStatus(ctx, run.ID, runStatus); err != nil {
		s.logger.Warn("failed to sync run status from objective event",
			"run_id", run.ID,
			"objective_id", event.Objective,
			"target_status", runStatus,
			"error", err,
		)
		return
	}

	s.logger.Info("run status synced from objective",
		"run_id", run.ID,
		"objective_id", event.Objective,
		"status", runStatus,
	)
}

func (s *Service) syncRunRecoveryStatus(ctx context.Context, event domain.Event) {
	run, err := s.ledger.GetRunByObjective(ctx, event.Objective)
	if err != nil {
		return
	}

	target := domain.RunStatusActive
	if event.Type == domain.EventRecoveryBlocked {
		target = domain.RunStatusBlocked
	}
	if run.Status == target || run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed || run.Status == domain.RunStatusPartial {
		return
	}
	if err := s.ledger.UpdateRunStatus(ctx, run.ID, target); err != nil {
		s.logger.Warn("failed to sync run status from recovery event", "run_id", run.ID, "objective_id", event.Objective, "target_status", target, "error", err)
		return
	}
	s.logger.Info("run status synced from recovery event", "run_id", run.ID, "objective_id", event.Objective, "status", target)
}

// resolveBlocked checks execution state to determine why a run is blocked.
func (s *Service) resolveBlocked(ctx context.Context, run *domain.Run) *domain.BlockedState {
	exec, err := s.ledger.GetExecutionByObjective(ctx, run.ObjectiveID)
	if err != nil {
		if block, ok := s.activeRecoveryBlock(ctx, run.ObjectiveID); ok {
			return block
		}
		return &domain.BlockedState{Kind: "unknown"}
	}
	if exec.Status == "waiting_human" {
		return &domain.BlockedState{Kind: "human_approval"}
	}
	if block, ok := s.activeRecoveryBlock(ctx, run.ObjectiveID); ok {
		return block
	}
	return &domain.BlockedState{Kind: "unknown"}
}

// resolveStreams gathers stream states for the objective's plan.
// For failed streams with an associated execution, it extracts the last error
// from the execution's StepStates and marks the stream as retryable.
func (s *Service) resolveStreams(ctx context.Context, objectiveID string) []domain.RunStreamState {
	plan, err := s.ledger.GetPlanByObjective(ctx, objectiveID)
	if err != nil {
		return nil
	}

	streams, err := s.ledger.ListStreamsByPlan(ctx, plan.ID)
	if err != nil {
		return nil
	}

	result := make([]domain.RunStreamState, len(streams))
	for i, st := range streams {
		rss := domain.RunStreamState{
			StreamID: st.ID,
			Title:    st.Title,
			Status:   st.Status,
		}

		// For failed streams, extract error from the execution and mark retryable.
		if st.Status == domain.StreamStatusFailed && st.ExecutionID != "" {
			rss.Retryable = true
			if exec, err := s.ledger.GetExecution(ctx, st.ExecutionID); err == nil {
				rss.Error = lastStepError(exec)
			}
		}

		result[i] = rss
	}
	return result
}

// lastStepError extracts the error message from the first failed step in an execution.
func lastStepError(exec *blueprint.Execution) string {
	for _, state := range exec.StepStates {
		if state.Status == blueprint.StepStatusFailed && state.Error != "" {
			return state.Error
		}
	}
	return ""
}
