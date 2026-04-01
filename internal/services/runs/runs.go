// Package runs implements run-centric orchestration.
//
// A Run is the durable aggregate that owns objective orchestration from start
// through completion. It hides internal execution details (sub-executions,
// agent sessions, merge queue bookkeeping) and exposes only semantic
// operations: start, intervene, inspect, recover.
//
// Migration direction: callers should migrate from helper-oriented flows
// (Execute, Approve, Retry, Kill plus indirect step handlers) to run-oriented
// flows (Start, Command, Snapshot, Run). This migration is incremental — the
// run boundary initially delegates to existing internals.
package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/dispatch"
)

// ErrInvalidState is returned when an operation is invalid for the current state.
var ErrInvalidState = errors.New("invalid state")

// Runs is the run-centric orchestration boundary.
//
// Start creates a new run for an objective and begins execution.
// Command sends an intervention (approve, retry, abort) to a run.
// Snapshot returns the observable state of a run at a point in time.
// Run starts the background recovery/reconciliation loop.
type Runs interface {
	Start(ctx context.Context, objectiveID string) (domain.Snapshot, error)
	Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error)
	Snapshot(ctx context.Context, runID string) (domain.Snapshot, error)
	Run(ctx context.Context) error
}

// Service implements the Runs boundary.
type Service struct {
	runs        *db.RunStore
	objectives  *db.ObjectiveStore
	plans       *db.PlanStore
	streams     *db.StreamStore
	executions  *db.ExecutionStore
	coordinator dispatch.Orchestrator
	logger      *slog.Logger
}

// New creates a new runs Service.
func New(
	runs *db.RunStore,
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	executions *db.ExecutionStore,
	coordinator dispatch.Orchestrator,
	logger *slog.Logger,
) *Service {
	return &Service{
		runs:        runs,
		objectives:  objectives,
		plans:       plans,
		streams:     streams,
		executions:  executions,
		coordinator: coordinator,
		logger:      logger.With("component", "runs"),
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

// Command sends an intervention to a run.
// Not yet implemented — will be wired in #24/#25.
func (s *Service) Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error) {
	return domain.Snapshot{}, fmt.Errorf("runs.Command not yet implemented")
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

// Run starts the background recovery/reconciliation loop.
// Not yet implemented — will be wired in #26.
func (s *Service) Run(ctx context.Context) error {
	return fmt.Errorf("runs.Run not yet implemented")
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
		result[i] = domain.RunStreamState{
			StreamID: st.ID,
			Title:    st.Title,
			Status:   st.Status,
		}
	}
	return result
}
