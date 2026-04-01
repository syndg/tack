// Package runs implements run-centric orchestration.
//
// A Run is the durable aggregate that owns objective orchestration from start
// through completion. It hides internal execution details (sub-executions,
// agent sessions, merge queue bookkeeping, scheduler state, merge progression)
// and exposes only semantic operations: start, intervene, inspect, recover.
//
// Ownership boundary: the runs service owns the lifecycle of all internal
// orchestration services. The coordinator (execution engine), merge processor,
// scheduler, and agent tracker are implementation details — callers interact
// only through Start, Command, Snapshot, and Run.
//
// The daemon no longer holds direct references to the coordinator, scheduler,
// spawner, or merge processor. All orchestration flows through this boundary:
//
//   - Start: creates a run and delegates to coordinator.Execute
//   - Command(approve): finds blocked execution, delegates to coordinator.Approve
//   - Command(retry): resolves failed stream execution, delegates to coordinator.Retry
//   - Command(abort): stops the coordinator, marks run failed
//   - Command(kill): terminates a specific agent session within a run
//   - Snapshot: assembles observable state from persisted records
//   - Run/Stop: manages coordinator and merge processor lifecycle
//
// Pre-migration escape hatches (ApproveExecution, RetryExecution, KillAgent)
// are provided for executions that predate the run API. These delegate directly
// to the coordinator without run-level state transitions and should be removed
// once all executions are created through Start.
//
// Recovery (issue #26):
//   - Run() reconciles in-flight runs on daemon restart. Active/blocked runs
//     are checked against their objective's current state: terminal objectives
//     sync immediately, waiting_human objectives mark the run blocked, and
//     executing objectives are left active (the coordinator resumes them).
//   - Orphaned runs (objective missing) are marked failed.
//
// Retry flow (issue #25):
//   - Snapshot exposes retryable failure information: failed streams with an
//     associated execution are marked Retryable=true and include the last
//     step error from the failed execution. Callers inspect the snapshot to
//     identify which streams can be retried and why they failed.
//   - Command(retry) routes through the run boundary with guidance, resolves
//     the failed execution from the stream, and delegates to the coordinator.
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/events"
)

// ErrInvalidState is returned when an operation is invalid for the current state.
var ErrInvalidState = errors.New("invalid state")

// Runs is the run-centric orchestration boundary.
//
// Start creates a new run for an objective and begins execution.
// Command sends an intervention (approve, retry, abort) to a run.
// Snapshot returns the observable state of a run at a point in time.
// Run starts the background orchestration loop (coordinator, merge processor,
// run status synchronization, and recovery/reconciliation).
type Runs interface {
	Start(ctx context.Context, objectiveID string) (domain.Snapshot, error)
	Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error)
	Snapshot(ctx context.Context, runID string) (domain.Snapshot, error)
	Run(ctx context.Context) error
	Stop()
}

// MergeService is the lifecycle boundary for the merge processor.
// Extracted as an interface so the runs package does not depend on
// the merge package directly.
type MergeService interface {
	Start(ctx context.Context) error
	Stop()
}

// Service implements the Runs boundary.
type Service struct {
	runs       *db.RunStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	executions *db.ExecutionStore
	agents     *db.AgentStore

	// Internal orchestration services — owned by runs, not exposed to callers.
	coordinator    dispatch.Orchestrator
	mergeProcessor MergeService
	eventBus       *events.PersistentBus

	logger *slog.Logger
}

// New creates a new runs Service.
func New(
	runs *db.RunStore,
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	executions *db.ExecutionStore,
	agents *db.AgentStore,
	coordinator dispatch.Orchestrator,
	mergeProcessor MergeService,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Service {
	return &Service{
		runs:           runs,
		objectives:     objectives,
		plans:          plans,
		streams:        streams,
		executions:     executions,
		agents:         agents,
		coordinator:    coordinator,
		mergeProcessor: mergeProcessor,
		eventBus:       eventBus,
		logger:         logger.With("component", "runs"),
	}
}

// Start creates a new run for an objective and begins execution.
// It validates the objective is in a startable state, creates a durable
// Run record, delegates execution to the coordinator, and returns a
// snapshot reflecting the run's initial state.
func (s *Service) Start(ctx context.Context, objectiveID string) (domain.Snapshot, error) {
	obj, err := s.objectives.Get(ctx, objectiveID)
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

	run := &domain.Run{ObjectiveID: objectiveID}
	if err := s.runs.Create(ctx, run); err != nil {
		return domain.Snapshot{}, fmt.Errorf("creating run: %w", err)
	}

	s.logger.Info("starting run", "run_id", run.ID, "objective_id", objectiveID)

	if err := s.coordinator.Execute(ctx, objectiveID); err != nil {
		// Mark run as failed since execution couldn't start.
		_ = s.runs.UpdateStatus(ctx, run.ID, domain.RunStatusFailed)
		return domain.Snapshot{}, fmt.Errorf("starting execution: %w", err)
	}

	snap, err := s.Snapshot(ctx, run.ID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("building initial snapshot: %w", err)
	}

	return snap, nil
}

// Command sends an intervention (approve, retry, abort) to a run.
//
// This is the single entry point for all run interventions. It owns the
// choreography that was previously spread across coordinator.Approve,
// coordinator.Retry, and coordinator.Kill — callers no longer need to
// understand execution IDs, session IDs, or internal state machines.
//
// The coordinator remains the execution engine, but Command owns the
// run-level state transitions and delegates internally.
func (s *Service) Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error) {
	run, err := s.runs.Get(ctx, runID)
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
	case domain.CommandKill:
		return s.commandKill(ctx, run, cmd)
	default:
		return domain.Snapshot{}, fmt.Errorf("unknown command kind %q: %w", cmd.Kind, ErrInvalidState)
	}
}

// commandApprove handles the approve intervention.
// Finds the blocked execution for the run's objective and delegates to
// coordinator.Approve, then updates run status from blocked → active.
func (s *Service) commandApprove(ctx context.Context, run *domain.Run) (domain.Snapshot, error) {
	// Find the execution that's waiting for human approval.
	exec, err := s.executions.GetByObjective(ctx, run.ObjectiveID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("finding execution for objective %s: %w", run.ObjectiveID, err)
	}
	if exec.Status != "waiting_human" {
		return domain.Snapshot{}, fmt.Errorf(
			"run %s has no execution waiting for approval (status: %s): %w",
			run.ID, exec.Status, ErrInvalidState,
		)
	}

	if err := s.coordinator.Approve(ctx, exec.ID); err != nil {
		return domain.Snapshot{}, fmt.Errorf("approving execution: %w", err)
	}

	// Transition run back to active now that it's unblocked.
	if run.Status == domain.RunStatusBlocked {
		if err := s.runs.UpdateStatus(ctx, run.ID, domain.RunStatusActive); err != nil {
			s.logger.Warn("failed to update run status after approve",
				"run_id", run.ID, "error", err)
		}
	}

	s.logger.Info("run approved", "run_id", run.ID, "execution_id", exec.ID)
	return s.Snapshot(ctx, run.ID)
}

// commandRetry handles the retry intervention for a failed stream.
// Resolves the failed sub-execution from the stream, delegates to
// coordinator.Retry, and ensures the run stays in active status.
func (s *Service) commandRetry(ctx context.Context, run *domain.Run, cmd domain.Command) (domain.Snapshot, error) {
	if cmd.StreamID == "" {
		return domain.Snapshot{}, fmt.Errorf("retry command requires stream_id: %w", ErrInvalidState)
	}

	// Find the failed sub-execution for this stream.
	stream, err := s.streams.Get(ctx, cmd.StreamID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading stream %s: %w", cmd.StreamID, err)
	}
	if stream.Status != "failed" {
		return domain.Snapshot{}, fmt.Errorf(
			"stream %s is not failed (status: %s): %w",
			cmd.StreamID, stream.Status, ErrInvalidState,
		)
	}
	if stream.ExecutionID == "" {
		return domain.Snapshot{}, fmt.Errorf("stream %s has no execution to retry: %w", cmd.StreamID, ErrInvalidState)
	}

	if err := s.coordinator.Retry(ctx, stream.ExecutionID, cmd.Guidance); err != nil {
		return domain.Snapshot{}, fmt.Errorf("retrying stream execution: %w", err)
	}

	// Ensure run is marked active (it may be in partial/failed state from
	// the original execution completing with failures).
	if run.Status != domain.RunStatusActive {
		if err := s.runs.UpdateStatus(ctx, run.ID, domain.RunStatusActive); err != nil {
			s.logger.Warn("failed to update run status after retry",
				"run_id", run.ID, "error", err)
		}
	}

	s.logger.Info("run stream retry initiated",
		"run_id", run.ID, "stream_id", cmd.StreamID, "has_guidance", cmd.Guidance != "")
	return s.Snapshot(ctx, run.ID)
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

	// Stop the coordinator's active execution for this objective.
	// This cancels the execution context and kills tracked agents.
	s.coordinator.Stop()

	if err := s.runs.UpdateStatus(ctx, run.ID, domain.RunStatusFailed); err != nil {
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
	session, err := s.agents.Get(ctx, cmd.SessionID)
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

// KillAgent terminates an agent session directly through the coordinator.
// This is a low-level escape hatch for agent sessions that predate the run API.
// Prefer Command(kill) when a run exists for the agent's objective.
func (s *Service) KillAgent(ctx context.Context, sessionID string) error {
	return s.coordinator.Kill(ctx, sessionID)
}

// ApproveExecution approves a human gate directly through the coordinator.
// This is a low-level escape hatch for executions that predate the run API.
// Prefer Command(approve) when a run exists for the execution's objective.
func (s *Service) ApproveExecution(ctx context.Context, executionID string) error {
	return s.coordinator.Approve(ctx, executionID)
}

// RetryExecution retries a failed execution directly through the coordinator.
// This is a low-level escape hatch for executions that predate the run API.
// Prefer Command(retry) when a run exists for the execution's objective.
func (s *Service) RetryExecution(ctx context.Context, executionID string, guidance string) error {
	return s.coordinator.Retry(ctx, executionID, guidance)
}

// Snapshot returns the observable state of a run by assembling current
// persisted state from the run record, objective, plan, streams, and
// execution.
func (s *Service) Snapshot(ctx context.Context, runID string) (domain.Snapshot, error) {
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("loading run: %w", err)
	}

	snap := domain.Snapshot{
		RunID:       run.ID,
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

	return snap, nil
}

// SnapshotByObjective returns a snapshot for the most recent run of an objective.
func (s *Service) SnapshotByObjective(ctx context.Context, objectiveID string) (domain.Snapshot, error) {
	run, err := s.runs.GetByObjective(ctx, objectiveID)
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
	s.recoverRuns(ctx)

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
				}
			}
		}
	}()

	s.logger.Info("runs orchestration loop started")
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
	activeRuns, err := s.runs.ListActive(ctx)
	if err != nil {
		s.logger.Warn("failed to list active runs for recovery", "error", err)
		return
	}

	if len(activeRuns) == 0 {
		return
	}

	s.logger.Info("recovering in-flight runs", "count", len(activeRuns))

	for _, run := range activeRuns {
		obj, err := s.objectives.Get(ctx, run.ObjectiveID)
		if err != nil {
			// Objective missing — mark run as failed so it doesn't stay orphaned.
			s.logger.Warn("objective not found during run recovery, marking run failed",
				"run_id", run.ID, "objective_id", run.ObjectiveID, "error", err)
			_ = s.runs.UpdateStatus(ctx, run.ID, domain.RunStatusFailed)
			continue
		}

		var newStatus domain.RunStatus
		switch obj.Status {
		case domain.ObjectiveStatusCompleted:
			newStatus = domain.RunStatusCompleted
		case domain.ObjectiveStatusPartial:
			newStatus = domain.RunStatusPartial
		case domain.ObjectiveStatusFailed:
			newStatus = domain.RunStatusFailed
		case domain.ObjectiveStatusExecuting:
			// Coordinator's recoverExecutions handles resuming the execution.
			// Check if there's a waiting_human execution that should block the run.
			if exec, err := s.executions.GetByObjective(ctx, run.ObjectiveID); err == nil {
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

		if err := s.runs.UpdateStatus(ctx, run.ID, newStatus); err != nil {
			s.logger.Warn("failed to update run during recovery",
				"run_id", run.ID, "target_status", newStatus, "error", err)
			continue
		}

		s.logger.Info("recovered run",
			"run_id", run.ID, "objective_id", run.ObjectiveID,
			"old_status", run.Status, "new_status", newStatus)
	}
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
		runStatus = domain.RunStatusPartial
	case domain.ObjectiveStatusFailed:
		runStatus = domain.RunStatusFailed
	default:
		// Non-terminal transitions (planning, approved, executing) don't
		// change the run status — the run stays active.
		return
	}

	run, err := s.runs.GetByObjective(ctx, event.Objective)
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

	if err := s.runs.UpdateStatus(ctx, run.ID, runStatus); err != nil {
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

// resolveBlocked checks execution state to determine why a run is blocked.
func (s *Service) resolveBlocked(ctx context.Context, run *domain.Run) *domain.BlockedState {
	exec, err := s.executions.GetByObjective(ctx, run.ObjectiveID)
	if err != nil {
		return &domain.BlockedState{Kind: "unknown"}
	}
	if exec.Status == "waiting_human" {
		return &domain.BlockedState{Kind: "human_approval"}
	}
	return &domain.BlockedState{Kind: "unknown"}
}

// resolveStreams gathers stream states for the objective's plan.
// For failed streams with an associated execution, it extracts the last error
// from the execution's StepStates and marks the stream as retryable.
func (s *Service) resolveStreams(ctx context.Context, objectiveID string) []domain.RunStreamState {
	plan, err := s.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return nil
	}

	streams, err := s.streams.ListByPlan(ctx, plan.ID)
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
			if exec, err := s.executions.Get(ctx, st.ExecutionID); err == nil {
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
