package runs

import (
	"context"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
)

// RunLedger is the run-shaped persistence seam used by the runtime boundary.
// Production adapts the existing stores here; tests can substitute local
// in-memory state without wiring every underlying store into the boundary.
type RunLedger interface {
	GetRun(ctx context.Context, runID string) (*domain.Run, error)
	GetRunByObjective(ctx context.Context, objectiveID string) (*domain.Run, error)
	CreateRun(ctx context.Context, run *domain.Run) error
	UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus) error
	ListActiveRuns(ctx context.Context, projectID string) ([]domain.Run, error)

	GetObjective(ctx context.Context, objectiveID string) (*domain.Objective, error)
	GetPlanByObjective(ctx context.Context, objectiveID string) (*domain.Plan, error)
	GetStream(ctx context.Context, streamID string) (*domain.Stream, error)
	ListStreamsByPlan(ctx context.Context, planID string) ([]domain.Stream, error)
	UpdateStreamFileScope(ctx context.Context, streamID string, fileScope []string) error

	GetExecution(ctx context.Context, executionID string) (*blueprint.Execution, error)
	GetExecutionByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error)

	GetAgentSession(ctx context.Context, sessionID string) (*domain.AgentSession, error)
	ListAgentSessionsByObjective(ctx context.Context, objectiveID string) ([]domain.AgentSession, error)

	ListAttemptsByStream(ctx context.Context, streamID string) ([]domain.Attempt, error)
}

type storeRunLedger struct {
	runs       *db.RunStore
	attempts   *db.AttemptStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	executions *db.ExecutionStore
	agents     *db.AgentStore
}

func (l *storeRunLedger) GetRun(ctx context.Context, runID string) (*domain.Run, error) {
	return l.runs.Get(ctx, runID)
}

func (l *storeRunLedger) GetRunByObjective(ctx context.Context, objectiveID string) (*domain.Run, error) {
	return l.runs.GetByObjective(ctx, objectiveID)
}

func (l *storeRunLedger) CreateRun(ctx context.Context, run *domain.Run) error {
	return l.runs.Create(ctx, run)
}

func (l *storeRunLedger) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus) error {
	return l.runs.UpdateStatus(ctx, runID, status)
}

func (l *storeRunLedger) ListActiveRuns(ctx context.Context, projectID string) ([]domain.Run, error) {
	if projectID != "" {
		return l.runs.ListActiveByProject(ctx, projectID)
	}
	return l.runs.ListActive(ctx)
}

func (l *storeRunLedger) GetObjective(ctx context.Context, objectiveID string) (*domain.Objective, error) {
	return l.objectives.Get(ctx, objectiveID)
}

func (l *storeRunLedger) GetPlanByObjective(ctx context.Context, objectiveID string) (*domain.Plan, error) {
	return l.plans.GetByObjective(ctx, objectiveID)
}

func (l *storeRunLedger) GetStream(ctx context.Context, streamID string) (*domain.Stream, error) {
	return l.streams.Get(ctx, streamID)
}

func (l *storeRunLedger) ListStreamsByPlan(ctx context.Context, planID string) ([]domain.Stream, error) {
	return l.streams.ListByPlan(ctx, planID)
}

func (l *storeRunLedger) UpdateStreamFileScope(ctx context.Context, streamID string, fileScope []string) error {
	return l.streams.UpdateFileScope(ctx, streamID, fileScope)
}

func (l *storeRunLedger) GetExecution(ctx context.Context, executionID string) (*blueprint.Execution, error) {
	return l.executions.Get(ctx, executionID)
}

func (l *storeRunLedger) GetExecutionByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error) {
	return l.executions.GetByObjective(ctx, objectiveID)
}

func (l *storeRunLedger) GetAgentSession(ctx context.Context, sessionID string) (*domain.AgentSession, error) {
	return l.agents.Get(ctx, sessionID)
}

func (l *storeRunLedger) ListAgentSessionsByObjective(ctx context.Context, objectiveID string) ([]domain.AgentSession, error) {
	return l.agents.ListByObjective(ctx, objectiveID)
}

func (l *storeRunLedger) ListAttemptsByStream(ctx context.Context, streamID string) ([]domain.Attempt, error) {
	if l.attempts == nil {
		return nil, nil
	}
	return l.attempts.ListByStream(ctx, streamID)
}
