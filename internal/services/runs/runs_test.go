package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

type memoryRunLedger struct {
	runs       map[string]*domain.Run
	objectives map[string]*domain.Objective
}

func newMemoryRunLedger() *memoryRunLedger {
	return &memoryRunLedger{runs: map[string]*domain.Run{}, objectives: map[string]*domain.Objective{}}
}

func (l *memoryRunLedger) GetRun(ctx context.Context, runID string) (*domain.Run, error) {
	if run, ok := l.runs[runID]; ok {
		copy := *run
		return &copy, nil
	}
	return nil, fmt.Errorf("run not found: %s", runID)
}

func (l *memoryRunLedger) GetRunByObjective(ctx context.Context, objectiveID string) (*domain.Run, error) {
	for _, run := range l.runs {
		if run.ObjectiveID == objectiveID {
			copy := *run
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("run not found for objective: %s", objectiveID)
}

func (l *memoryRunLedger) CreateRun(ctx context.Context, run *domain.Run) error {
	if run.ID == "" {
		run.ID = fmt.Sprintf("run-%d", len(l.runs)+1)
	}
	if run.Status == "" {
		run.Status = domain.RunStatusActive
	}
	copy := *run
	l.runs[run.ID] = &copy
	return nil
}

func (l *memoryRunLedger) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus) error {
	run, ok := l.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	run.Status = status
	return nil
}

func (l *memoryRunLedger) ListActiveRuns(ctx context.Context, projectID string) ([]domain.Run, error) {
	var runs []domain.Run
	for _, run := range l.runs {
		if run.Status == domain.RunStatusActive || run.Status == domain.RunStatusBlocked {
			runs = append(runs, *run)
		}
	}
	return runs, nil
}

func (l *memoryRunLedger) GetObjective(ctx context.Context, objectiveID string) (*domain.Objective, error) {
	obj, ok := l.objectives[objectiveID]
	if !ok {
		return nil, fmt.Errorf("objective not found: %s", objectiveID)
	}
	copy := *obj
	return &copy, nil
}

func (l *memoryRunLedger) GetPlanByObjective(ctx context.Context, objectiveID string) (*domain.Plan, error) {
	return nil, fmt.Errorf("plan not found for objective: %s", objectiveID)
}

func (l *memoryRunLedger) GetStream(ctx context.Context, streamID string) (*domain.Stream, error) {
	return nil, fmt.Errorf("stream not found: %s", streamID)
}

func (l *memoryRunLedger) ListStreamsByPlan(ctx context.Context, planID string) ([]domain.Stream, error) {
	return nil, fmt.Errorf("streams not found for plan: %s", planID)
}

func (l *memoryRunLedger) UpdateStreamFileScope(ctx context.Context, streamID string, fileScope []string) error {
	return fmt.Errorf("stream not found: %s", streamID)
}

func (l *memoryRunLedger) GetExecution(ctx context.Context, executionID string) (*blueprint.Execution, error) {
	return nil, fmt.Errorf("execution not found: %s", executionID)
}

func (l *memoryRunLedger) GetExecutionByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error) {
	return nil, fmt.Errorf("execution not found for objective: %s", objectiveID)
}

func (l *memoryRunLedger) GetAgentSession(ctx context.Context, sessionID string) (*domain.AgentSession, error) {
	return nil, fmt.Errorf("agent session not found: %s", sessionID)
}

func (l *memoryRunLedger) ListAgentSessionsByObjective(ctx context.Context, objectiveID string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (l *memoryRunLedger) ListAttemptsByStream(ctx context.Context, streamID string) ([]domain.Attempt, error) {
	return nil, nil
}

// mockOrchestrator is a minimal test double for dispatch.Orchestrator.
type mockOrchestrator struct {
	startCalled bool

	executeCalled bool
	executeID     string
	executeErr    error

	approveCalled bool
	approveID     string
	approveErr    error

	retryCalled       bool
	retryID           string
	retryGuidance     string
	retryErr          error
	retryStreamCalled bool
	retryStreamID     string

	stopCalled       bool
	abortCalled      bool
	abortObjectiveID string
	abortErr         error
	killCalled       bool
	killSessionID    string
	killErr          error
}

func (m *mockOrchestrator) Start(ctx context.Context) error { m.startCalled = true; return nil }
func (m *mockOrchestrator) Stop()                           { m.stopCalled = true }
func (m *mockOrchestrator) Approve(ctx context.Context, executionID string) error {
	m.approveCalled = true
	m.approveID = executionID
	return m.approveErr
}
func (m *mockOrchestrator) Retry(ctx context.Context, id string, g string) error {
	m.retryCalled = true
	m.retryID = id
	m.retryGuidance = g
	return m.retryErr
}
func (m *mockOrchestrator) RetryStream(ctx context.Context, id string, g string) error {
	m.retryStreamCalled = true
	m.retryStreamID = id
	m.retryGuidance = g
	return m.retryErr
}
func (m *mockOrchestrator) Abort(ctx context.Context, objectiveID string) error {
	m.abortCalled = true
	m.abortObjectiveID = objectiveID
	return m.abortErr
}
func (m *mockOrchestrator) Kill(ctx context.Context, sessionID string) error {
	m.killCalled = true
	m.killSessionID = sessionID
	return m.killErr
}
func (m *mockOrchestrator) Execute(ctx context.Context, objectiveID string) error {
	m.executeCalled = true
	m.executeID = objectiveID
	return m.executeErr
}

// mockMergeService is a minimal test double for MergeOrchestrator.
type mockMergeService struct {
	started bool
	stopped bool
}

func (m *mockMergeService) Start(ctx context.Context) error                 { m.started = true; return nil }
func (m *mockMergeService) Stop()                                           { m.stopped = true }
func (m *mockMergeService) EnqueueStream(_ context.Context, _ string) error { return nil }
func (m *mockMergeService) MergerSandboxID(_ string) string                 { return "" }
func (m *mockMergeService) ResetMergingEntries(_ context.Context, _ string) {}

// newTestService is a test helper that wraps New() with a Config using
// a pre-built orchestrator (test mock). Mirrors the old New() parameter order.
func newTestServiceWithAttempts(t *testing.T,
	runStore *db.RunStore, attemptStore *db.AttemptStore, objectiveStore *db.ObjectiveStore, planStore *db.PlanStore,
	streamStore *db.StreamStore, executionStore *db.ExecutionStore, agentStore *db.AgentStore,
	orch dispatch.Orchestrator, merger MergeOrchestrator,
	eventBus *events.PersistentBus, logger *slog.Logger,
) *Service {
	t.Helper()
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: merger,
		Runs:           runStore,
		Attempts:       attemptStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       eventBus,
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func newTestService(t *testing.T,
	runStore *db.RunStore, objectiveStore *db.ObjectiveStore, planStore *db.PlanStore,
	streamStore *db.StreamStore, executionStore *db.ExecutionStore, agentStore *db.AgentStore,
	orch dispatch.Orchestrator, merger MergeOrchestrator,
	eventBus *events.PersistentBus, logger *slog.Logger,
) *Service {
	t.Helper()
	return newTestServiceWithAttempts(
		t,
		runStore,
		nil,
		objectiveStore,
		planStore,
		streamStore,
		executionStore,
		agentStore,
		orch,
		merger,
		eventBus,
		logger,
	)
}

// openTestDB opens a fresh SQLite DB with migrations applied.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.NewProjectStore(d.Conn()).Upsert(context.Background(), &domain.Project{ID: "test-project", Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir() + "/.tack/config.yaml"}); err != nil {
		t.Fatalf("register test project: %v", err)
	}
	return d
}

// newTestEventBus creates a PersistentBus backed by a real event store.
func newTestEventBus(t *testing.T, database *db.DB) *events.PersistentBus {
	t.Helper()
	eventStore := db.NewEventStore(database.Conn())
	return events.NewPersistentBus(eventStore, slog.Default())
}

func TestSnapshotCreateAndLoad(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	// Create an objective.
	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	// Create a plan for the objective.
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	// Create streams for the plan.
	streamA := &domain.Stream{PlanID: plan.ID, Title: "stream-a", Description: "first stream"}
	streamB := &domain.Stream{PlanID: plan.ID, Title: "stream-b", Description: "second stream"}
	if err := streamStore.Create(ctx, streamA); err != nil {
		t.Fatalf("creating stream A: %v", err)
	}
	if err := streamStore.Create(ctx, streamB); err != nil {
		t.Fatalf("creating stream B: %v", err)
	}

	// Create a run for the objective.
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Load snapshot and verify it reflects persisted state.
	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if snap.RunID != run.ID {
		t.Errorf("RunID = %q, want %q", snap.RunID, run.ID)
	}
	if snap.ObjectiveID != obj.ID {
		t.Errorf("ObjectiveID = %q, want %q", snap.ObjectiveID, obj.ID)
	}
	if snap.Status != domain.RunStatusActive {
		t.Errorf("Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
	if snap.Blocked != nil {
		t.Errorf("Blocked = %v, want nil", snap.Blocked)
	}
	if snap.Outcome != nil {
		t.Errorf("Outcome = %v, want nil", snap.Outcome)
	}
	if len(snap.Streams) != 2 {
		t.Fatalf("len(Streams) = %d, want 2", len(snap.Streams))
	}
	if snap.Streams[0].Title != "stream-a" {
		t.Errorf("Streams[0].Title = %q, want %q", snap.Streams[0].Title, "stream-a")
	}
	if snap.Streams[1].Title != "stream-b" {
		t.Errorf("Streams[1].Title = %q, want %q", snap.Streams[1].Title, "stream-b")
	}
	if snap.Streams[0].Status != domain.StreamStatusPending {
		t.Errorf("Streams[0].Status = %q, want %q", snap.Streams[0].Status, domain.StreamStatusPending)
	}
}

func TestSnapshotByObjective(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	// Create objective + run.
	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	snap, err := svc.SnapshotByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("SnapshotByObjective: %v", err)
	}
	if snap.RunID != run.ID {
		t.Errorf("RunID = %q, want %q", snap.RunID, run.ID)
	}
}

func TestSyncRunRecoveryStatusMarksRunBlocked(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()

	runStore := db.NewRunStore(conn)
	attemptStore := db.NewAttemptStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	svc := newTestServiceWithAttempts(t, runStore, attemptStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), slog.Default())

	obj := &domain.Objective{ID: "obj-recovery-block", Description: "blocked objective", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusActive}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	svc.syncRunRecoveryStatus(ctx, domain.Event{Type: domain.EventRecoveryBlocked, Objective: obj.ID})

	updated, err := runStore.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updated.Status != domain.RunStatusBlocked {
		t.Fatalf("run status = %s, want blocked", updated.Status)
	}
}

func TestSnapshotTerminalOutcome(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Transition run to completed.
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusCompleted {
		t.Errorf("Status = %q, want %q", snap.Status, domain.RunStatusCompleted)
	}
	if snap.Outcome == nil {
		t.Fatal("Outcome is nil, want non-nil")
	}
	if snap.Outcome.Status != domain.RunStatusCompleted {
		t.Errorf("Outcome.Status = %q, want %q", snap.Outcome.Status, domain.RunStatusCompleted)
	}
}

func TestSnapshotNotFound(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	_, err := svc.Snapshot(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent run, got nil")
	}
}

func TestStartCreatesRunAndDelegates(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       newTestEventBus(t, database),
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create an objective in "approved" state (startable).
	obj := &domain.Objective{Description: "test start", Status: domain.ObjectiveStatusApproved}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	snap, err := svc.Start(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify run was created and snapshot reflects it.
	if snap.RunID == "" {
		t.Error("RunID is empty")
	}
	if snap.ObjectiveID != obj.ID {
		t.Errorf("ObjectiveID = %q, want %q", snap.ObjectiveID, obj.ID)
	}
	if snap.Status != domain.RunStatusActive {
		t.Errorf("Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}

	// Verify coordinator.Execute was called with the right objective.
	if !orch.executeCalled {
		t.Error("coordinator.Execute was not called")
	}
	if orch.executeID != obj.ID {
		t.Errorf("coordinator.Execute called with %q, want %q", orch.executeID, obj.ID)
	}

	// Verify run is persisted in the store.
	run, err := runStore.GetByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if run.ID != snap.RunID {
		t.Errorf("persisted run ID = %q, want %q", run.ID, snap.RunID)
	}
}

func TestEnsureStartsRunAndReturnsRunView(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "ensure start", Status: domain.ObjectiveStatusApproved}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	view, err := svc.Ensure(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if view.RunID == "" {
		t.Fatal("RunID is empty")
	}
	if view.ObjectiveID != obj.ID {
		t.Fatalf("ObjectiveID = %q, want %q", view.ObjectiveID, obj.ID)
	}
	if view.Status != domain.RunStatusActive {
		t.Fatalf("Status = %q, want %q", view.Status, domain.RunStatusActive)
	}
	if !orch.executeCalled {
		t.Fatal("coordinator.Execute was not called")
	}
	if orch.executeID != obj.ID {
		t.Fatalf("coordinator.Execute called with %q, want %q", orch.executeID, obj.ID)
	}
}

func TestEnsureUsesSubstitutableRunLedger(t *testing.T) {
	ctx := context.Background()
	ledger := newMemoryRunLedger()
	ledger.objectives["obj-ledger"] = &domain.Objective{ID: "obj-ledger", Status: domain.ObjectiveStatusApproved}

	orch := &mockOrchestrator{}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Ledger:         ledger,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	view, err := svc.Ensure(ctx, "obj-ledger")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if view.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", view.RunID)
	}
	if view.Status != domain.RunStatusActive {
		t.Fatalf("Status = %q, want %q", view.Status, domain.RunStatusActive)
	}
	if !orch.executeCalled || orch.executeID != "obj-ledger" {
		t.Fatalf("Execute called=%v objective=%q, want obj-ledger", orch.executeCalled, orch.executeID)
	}
}

func TestEnsureResumesExistingRunWithoutDuplicateStart(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "ensure resume", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusActive}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	view, err := svc.Ensure(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if view.RunID != run.ID {
		t.Fatalf("RunID = %q, want existing %q", view.RunID, run.ID)
	}
	if view.Status != domain.RunStatusActive {
		t.Fatalf("Status = %q, want %q", view.Status, domain.RunStatusActive)
	}
	if orch.executeCalled {
		t.Fatal("coordinator.Execute was called for existing run")
	}
}

func TestStartRejectsExecutingObjective(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	// Create an objective already executing.
	obj := &domain.Objective{Description: "already running", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	_, err := svc.Start(ctx, obj.ID)
	if err == nil {
		t.Fatal("expected error starting executing objective, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got: %v", err)
	}
}

func TestStartMarksRunFailedOnExecuteError(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{executeErr: errors.New("blueprint not found")}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       newTestEventBus(t, database),
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obj := &domain.Objective{Description: "will fail", Status: domain.ObjectiveStatusApproved}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	_, err = svc.Start(ctx, obj.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify the run was created and marked as failed.
	run, err := runStore.GetByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if run.Status != domain.RunStatusFailed {
		t.Errorf("run status = %q, want %q", run.Status, domain.RunStatusFailed)
	}
}

func TestRunStartsInternalServices(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	merger := &mockMergeService{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, merger, newTestEventBus(t, database), logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := svc.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify internal services were started.
	if !merger.started {
		t.Error("merge processor was not started")
	}

	svc.Stop()

	if !merger.stopped {
		t.Error("merge processor was not stopped")
	}
}

func TestRunStatusSyncsOnObjectiveCompleted(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

	// Start the orchestration loop.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	// Create objective + run.
	obj := &domain.Objective{Description: "sync test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Simulate objective completing by emitting the event directly.
	eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusCompleted),
	)

	// Give the event loop time to process.
	time.Sleep(50 * time.Millisecond)

	// Verify run status was synced.
	updated, err := runStore.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != domain.RunStatusCompleted {
		t.Errorf("run status = %q, want %q", updated.Status, domain.RunStatusCompleted)
	}
}

func TestRunStatusSyncsOnObjectivePartial(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	obj := &domain.Objective{Description: "partial test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusPartial),
	)

	time.Sleep(50 * time.Millisecond)

	updated, err := runStore.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != domain.RunStatusPartial {
		t.Errorf("run status = %q, want %q", updated.Status, domain.RunStatusPartial)
	}
}

func TestRunStatusSyncsOnObjectiveFailed(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	obj := &domain.Objective{Description: "fail test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusFailed),
	)

	time.Sleep(50 * time.Millisecond)

	updated, err := runStore.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != domain.RunStatusFailed {
		t.Errorf("run status = %q, want %q", updated.Status, domain.RunStatusFailed)
	}
}

func TestRunStatusIgnoresNonTerminalTransitions(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	obj := &domain.Objective{Description: "non-terminal test", Status: domain.ObjectiveStatusPlanning}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Emit a non-terminal transition — should not change run status.
	eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusPlanning),
		"to", string(domain.ObjectiveStatusExecuting),
	)

	time.Sleep(50 * time.Millisecond)

	updated, err := runStore.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != domain.RunStatusActive {
		t.Errorf("run status = %q, want %q (should not change on non-terminal transition)", updated.Status, domain.RunStatusActive)
	}
}

// --- Command tests ---

func TestCommandApproveUnblocksRun(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       newTestEventBus(t, database),
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Setup: objective + run (blocked) + execution (waiting_human).
	obj := &domain.Objective{Description: "approve test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	exec := &blueprint.Execution{
		ID:          "exec-1",
		ObjectiveID: obj.ID,
		Status:      "waiting_human",
	}
	if err := executionStore.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusBlocked); err != nil {
		t.Fatalf("updating run to blocked: %v", err)
	}

	// Approve through Command.
	snap, err := svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandApprove})
	if err != nil {
		t.Fatalf("Command(approve): %v", err)
	}

	// Verify coordinator.Approve was called.
	if !orch.approveCalled {
		t.Error("coordinator.Approve was not called")
	}
	if orch.approveID != "exec-1" {
		t.Errorf("coordinator.Approve called with %q, want %q", orch.approveID, "exec-1")
	}

	// Verify run transitioned to active.
	if snap.Status != domain.RunStatusActive {
		t.Errorf("snap.Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
}

func TestCommandApproveRejectsNonWaitingExecution(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "approve reject test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	exec := &blueprint.Execution{
		ID:          "exec-2",
		ObjectiveID: obj.ID,
		Status:      "running",
	}
	if err := executionStore.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	_, err := svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandApprove})
	if err == nil {
		t.Fatal("expected error approving non-waiting execution, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got: %v", err)
	}
}

func TestCommandRetryDelegatesToCoordinator(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	insightStore := db.NewObjectiveInsightStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Runs:           runStore,
		Insights:       insightStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       newTestEventBus(t, database),
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Setup: objective + plan + failed stream with execution.
	obj := &domain.Objective{Description: "retry test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	stream := &domain.Stream{PlanID: plan.ID, Title: "failed-stream", Description: "will fail"}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}

	// Create a sub-execution for this stream.
	subExec := &blueprint.Execution{
		ID:          "sub-exec-1",
		ObjectiveID: obj.ID,
		ParentID:    "parent-exec",
		StreamID:    stream.ID,
		Status:      "failed",
	}
	if err := executionStore.Create(ctx, subExec); err != nil {
		t.Fatalf("creating sub-execution: %v", err)
	}

	// Mark stream as failed with execution reference.
	if err := streamStore.UpdateStatus(ctx, stream.ID, "failed"); err != nil {
		t.Fatalf("marking stream failed: %v", err)
	}
	if err := streamStore.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		t.Fatalf("setting stream execution ID: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	// Mark run as partial (simulating some streams completed, one failed).
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusPartial); err != nil {
		t.Fatalf("updating run to partial: %v", err)
	}

	// Retry through Command.
	snap, err := svc.Command(ctx, run.ID, domain.Command{
		Kind:           domain.CommandRetry,
		StreamID:       stream.ID,
		Guidance:       "try a different approach",
		ScopeAdditions: []string{"src/runtime.go", "src/config.go"},
	})
	if err != nil {
		t.Fatalf("Command(retry): %v", err)
	}

	// Verify coordinator.Retry was called with correct args.
	if !orch.retryCalled {
		t.Error("coordinator.Retry was not called")
	}
	if orch.retryID != subExec.ID {
		t.Errorf("coordinator.Retry called with %q, want %q", orch.retryID, subExec.ID)
	}
	if orch.retryGuidance != "try a different approach" {
		t.Errorf("coordinator.Retry guidance = %q, want %q", orch.retryGuidance, "try a different approach")
	}

	// Verify run transitioned back to active.
	if snap.Status != domain.RunStatusActive {
		t.Errorf("snap.Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
	insights, err := insightStore.ListByObjective(ctx, obj.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective insights: %v", err)
	}
	if len(insights) != 1 || insights[0].Kind != domain.InsightKindRetryGuidance {
		t.Fatalf("insights = %#v", insights)
	}
	updatedStream, err := streamStore.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	for _, want := range []string{"src/runtime.go", "src/config.go"} {
		found := false
		for _, got := range updatedStream.FileScope {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("file scope = %#v, missing %s", updatedStream.FileScope, want)
		}
	}
}

func TestCommandRetryWithoutExecutionDelegatesToStreamRetry(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	orch := &mockOrchestrator{}
	svc, err := New(Config{
		Orchestrator:   orch,
		MergeProcessor: &mockMergeService{},
		Runs:           runStore,
		Insights:       db.NewObjectiveInsightStore(conn),
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     db.NewExecutionStore(conn),
		Agents:         db.NewAgentStore(conn),
		EventBus:       newTestEventBus(t, database),
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obj := &domain.Objective{Description: "retry dependency-blocked stream", Status: domain.ObjectiveStatusPartial}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "dependency-blocked", Description: "never executed"}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	if err := streamStore.UpdateStatus(ctx, stream.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("marking stream failed: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusPartial}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	_, err = svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandRetry, StreamID: stream.ID, Guidance: "dependency is merged now"})
	if err != nil {
		t.Fatalf("Command(retry): %v", err)
	}
	if !orch.retryStreamCalled {
		t.Fatal("coordinator.RetryStream was not called")
	}
	if orch.retryStreamID != stream.ID {
		t.Fatalf("RetryStream id = %q, want %q", orch.retryStreamID, stream.ID)
	}
	if orch.retryCalled {
		t.Fatal("coordinator.Retry should not be called for stream without execution")
	}
}

func TestCommandRetryRequiresStreamID(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "retry no stream", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	_, err := svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandRetry})
	if err == nil {
		t.Fatal("expected error for retry without stream_id, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got: %v", err)
	}
}

func TestActAbortTargetsOneRunAndFailsRun(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "abort test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	snap, err := svc.Act(ctx, run.ID, domain.Command{
		Kind:   domain.CommandAbort,
		Reason: "test abort",
	})
	if err != nil {
		t.Fatalf("Act(abort): %v", err)
	}

	if !orch.abortCalled {
		t.Error("coordinator.Abort was not called")
	}
	if orch.abortObjectiveID != obj.ID {
		t.Errorf("Abort objective = %q, want %q", orch.abortObjectiveID, obj.ID)
	}
	if orch.stopCalled {
		t.Error("coordinator.Stop was called; abort should target one run")
	}

	// Verify run is marked failed.
	if snap.Status != domain.RunStatusFailed {
		t.Errorf("snap.Status = %q, want %q", snap.Status, domain.RunStatusFailed)
	}
	if snap.Outcome == nil {
		t.Fatal("snap.Outcome is nil, want non-nil")
	}
	if snap.Outcome.Status != domain.RunStatusFailed {
		t.Errorf("snap.Outcome.Status = %q, want %q", snap.Outcome.Status, domain.RunStatusFailed)
	}
}

func TestActKillWorkerTerminatesOneWorkerAndKeepsRunActive(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "kill worker test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	session := &domain.AgentSession{ObjectiveID: obj.ID, Role: domain.AgentRoleBuilder, Status: "running", SandboxID: "sb-worker"}
	if err := agentStore.Create(ctx, session); err != nil {
		t.Fatalf("creating agent session: %v", err)
	}

	snap, err := svc.Act(ctx, run.ID, domain.Command{Kind: domain.CommandKillWorker, SessionID: session.ID})
	if err != nil {
		t.Fatalf("Act(kill_worker): %v", err)
	}
	if !orch.killCalled {
		t.Fatal("coordinator.Kill was not called")
	}
	if orch.killSessionID != session.ID {
		t.Errorf("Kill session = %q, want %q", orch.killSessionID, session.ID)
	}
	if snap.Status != domain.RunStatusActive {
		t.Errorf("snap.Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
	if len(snap.Workers) != 1 {
		t.Fatalf("len(snap.Workers) = %d, want 1", len(snap.Workers))
	}
	if snap.Workers[0].SessionID != session.ID {
		t.Errorf("worker session = %q, want %q", snap.Workers[0].SessionID, session.ID)
	}
}

type blockingAbortOrchestrator struct {
	mockOrchestrator
	abortStarted chan struct{}
	releaseAbort chan struct{}
	killSeen     chan string
	abortOnce    sync.Once
	killOnce     sync.Once
}

func newBlockingAbortOrchestrator() *blockingAbortOrchestrator {
	return &blockingAbortOrchestrator{
		abortStarted: make(chan struct{}),
		releaseAbort: make(chan struct{}),
		killSeen:     make(chan string, 1),
	}
}

func (m *blockingAbortOrchestrator) Abort(ctx context.Context, objectiveID string) error {
	m.abortCalled = true
	m.abortObjectiveID = objectiveID
	m.abortOnce.Do(func() { close(m.abortStarted) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.releaseAbort:
		return m.abortErr
	}
}

func (m *blockingAbortOrchestrator) Kill(ctx context.Context, sessionID string) error {
	m.killCalled = true
	m.killSessionID = sessionID
	m.killOnce.Do(func() { m.killSeen <- sessionID })
	return m.killErr
}

func TestActSerializesConflictingMutationsForSameRun(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := newBlockingAbortOrchestrator()
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "same run serialization", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	session := &domain.AgentSession{ObjectiveID: obj.ID, Role: domain.AgentRoleBuilder, Status: "running", SandboxID: "sb-worker"}
	if err := agentStore.Create(ctx, session); err != nil {
		t.Fatalf("creating agent session: %v", err)
	}

	abortErr := make(chan error, 1)
	go func() {
		_, err := svc.Act(ctx, run.ID, domain.Command{Kind: domain.CommandAbort, Reason: "serialize test"})
		abortErr <- err
	}()

	select {
	case <-orch.abortStarted:
	case <-time.After(time.Second):
		t.Fatal("abort did not start")
	}

	killErr := make(chan error, 1)
	go func() {
		_, err := svc.Act(ctx, run.ID, domain.Command{Kind: domain.CommandKillWorker, SessionID: session.ID})
		killErr <- err
	}()

	select {
	case sessionID := <-orch.killSeen:
		t.Fatalf("kill for same run was not serialized; called with %s while abort was still active", sessionID)
	case <-time.After(100 * time.Millisecond):
	}

	close(orch.releaseAbort)

	if err := <-abortErr; err != nil {
		t.Fatalf("Act(abort): %v", err)
	}
	if err := <-killErr; err != nil {
		t.Fatalf("Act(kill_worker): %v", err)
	}
	select {
	case sessionID := <-orch.killSeen:
		if sessionID != session.ID {
			t.Fatalf("Kill session = %q, want %q", sessionID, session.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("kill did not run after same-run abort completed")
	}
}

func TestActAllowsConcurrentMutationsForDifferentRuns(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := newBlockingAbortOrchestrator()
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj1 := &domain.Objective{Description: "blocking run", Status: domain.ObjectiveStatusExecuting}
	obj2 := &domain.Objective{Description: "independent run", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj1); err != nil {
		t.Fatalf("creating objective 1: %v", err)
	}
	if err := objectiveStore.Create(ctx, obj2); err != nil {
		t.Fatalf("creating objective 2: %v", err)
	}
	run1 := &domain.Run{ObjectiveID: obj1.ID}
	run2 := &domain.Run{ObjectiveID: obj2.ID}
	if err := runStore.Create(ctx, run1); err != nil {
		t.Fatalf("creating run 1: %v", err)
	}
	if err := runStore.Create(ctx, run2); err != nil {
		t.Fatalf("creating run 2: %v", err)
	}
	session := &domain.AgentSession{ObjectiveID: obj2.ID, Role: domain.AgentRoleBuilder, Status: "running", SandboxID: "sb-worker"}
	if err := agentStore.Create(ctx, session); err != nil {
		t.Fatalf("creating agent session: %v", err)
	}

	abortErr := make(chan error, 1)
	go func() {
		_, err := svc.Act(ctx, run1.ID, domain.Command{Kind: domain.CommandAbort, Reason: "block run 1"})
		abortErr <- err
	}()

	select {
	case <-orch.abortStarted:
	case <-time.After(time.Second):
		t.Fatal("abort did not start")
	}

	killErr := make(chan error, 1)
	go func() {
		_, err := svc.Act(ctx, run2.ID, domain.Command{Kind: domain.CommandKillWorker, SessionID: session.ID})
		killErr <- err
	}()

	select {
	case sessionID := <-orch.killSeen:
		if sessionID != session.ID {
			t.Fatalf("Kill session = %q, want %q", sessionID, session.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("different-run kill was blocked by another run's abort")
	}
	if err := <-killErr; err != nil {
		t.Fatalf("Act(kill_worker): %v", err)
	}

	close(orch.releaseAbort)
	if err := <-abortErr; err != nil {
		t.Fatalf("Act(abort): %v", err)
	}
}

func TestCommandAbortRejectsTerminalRun(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "abort terminal", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusCompleted); err != nil {
		t.Fatalf("updating run to completed: %v", err)
	}

	_, err := svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandAbort})
	if err == nil {
		t.Fatal("expected error aborting terminal run, got nil")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got: %v", err)
	}
}

func TestSnapshotExposesRetryableFailureInfo(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

	// Setup: objective + plan + two streams (one completed, one failed with execution).
	obj := &domain.Objective{Description: "retryable info test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	okStream := &domain.Stream{PlanID: plan.ID, Title: "ok-stream", Description: "completed"}
	if err := streamStore.Create(ctx, okStream); err != nil {
		t.Fatalf("creating ok stream: %v", err)
	}
	if err := streamStore.UpdateStatus(ctx, okStream.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("executing stream: %v", err)
	}
	if err := streamStore.UpdateStatus(ctx, okStream.ID, domain.StreamStatusCompleted); err != nil {
		t.Fatalf("completing stream: %v", err)
	}

	failStream := &domain.Stream{PlanID: plan.ID, Title: "fail-stream", Description: "will fail"}
	if err := streamStore.Create(ctx, failStream); err != nil {
		t.Fatalf("creating fail stream: %v", err)
	}

	// Create a failed sub-execution with a step error.
	subExec := &blueprint.Execution{
		ID:          "sub-exec-fail",
		ObjectiveID: obj.ID,
		ParentID:    "parent-exec",
		StreamID:    failStream.ID,
		Status:      "failed",
		StepStates: map[string]*blueprint.StepState{
			"build_step": {
				StepID: "build_step",
				Status: blueprint.StepStatusFailed,
				Error:  "compilation failed: undefined variable foo",
			},
		},
	}
	if err := executionStore.Create(ctx, subExec); err != nil {
		t.Fatalf("creating sub-execution: %v", err)
	}

	// Mark stream as failed with execution reference.
	if err := streamStore.UpdateStatus(ctx, failStream.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("marking stream failed: %v", err)
	}
	if err := streamStore.UpdateExecutionID(ctx, failStream.ID, subExec.ID); err != nil {
		t.Fatalf("setting stream execution ID: %v", err)
	}

	// Create run.
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Take snapshot and verify retryable failure info.
	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if len(snap.Streams) != 2 {
		t.Fatalf("len(Streams) = %d, want 2", len(snap.Streams))
	}

	// Find the failed stream in the snapshot.
	var failState *domain.RunStreamState
	var okState *domain.RunStreamState
	for i := range snap.Streams {
		if snap.Streams[i].Title == "fail-stream" {
			failState = &snap.Streams[i]
		}
		if snap.Streams[i].Title == "ok-stream" {
			okState = &snap.Streams[i]
		}
	}

	if failState == nil {
		t.Fatal("failed stream not found in snapshot")
	}
	if failState.Status != domain.StreamStatusFailed {
		t.Errorf("failed stream status = %q, want %q", failState.Status, domain.StreamStatusFailed)
	}
	if !failState.Retryable {
		t.Error("failed stream should be marked as retryable")
	}
	if failState.Error != "compilation failed: undefined variable foo" {
		t.Errorf("failed stream error = %q, want %q", failState.Error, "compilation failed: undefined variable foo")
	}

	// Completed stream should NOT be retryable.
	if okState == nil {
		t.Fatal("ok stream not found in snapshot")
	}
	if okState.Retryable {
		t.Error("completed stream should not be retryable")
	}
	if okState.Error != "" {
		t.Errorf("completed stream error = %q, want empty", okState.Error)
	}
}

// --- Recovery tests (issue #26) ---

// TestRecoverRunsSyncsTerminalObjectives verifies that when the daemon restarts,
// active runs whose objectives reached a terminal state are reconciled correctly.
func TestRecoverRunsSyncsTerminalObjectives(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	// Setup: three objectives in different terminal states, each with an active run.
	tests := []struct {
		name          string
		objStatus     domain.ObjectiveStatus
		wantRunStatus domain.RunStatus
	}{
		{"completed", domain.ObjectiveStatusCompleted, domain.RunStatusCompleted},
		{"partial", domain.ObjectiveStatusPartial, domain.RunStatusPartial},
		{"failed", domain.ObjectiveStatusFailed, domain.RunStatusFailed},
	}

	runIDs := make([]string, len(tests))
	for i, tc := range tests {
		obj := &domain.Objective{Description: "recovery-" + tc.name, Status: tc.objStatus}
		if err := objectiveStore.Create(ctx, obj); err != nil {
			t.Fatalf("creating objective %s: %v", tc.name, err)
		}
		run := &domain.Run{ObjectiveID: obj.ID}
		if err := runStore.Create(ctx, run); err != nil {
			t.Fatalf("creating run %s: %v", tc.name, err)
		}
		runIDs[i] = run.ID
	}

	// Simulate restart: create a new service and call Run().
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	// Verify each run was reconciled to match its objective's terminal state.
	for i, tc := range tests {
		snap, err := svc.Snapshot(ctx, runIDs[i])
		if err != nil {
			t.Fatalf("Snapshot(%s): %v", tc.name, err)
		}
		if snap.Status != tc.wantRunStatus {
			t.Errorf("run %s: status = %q, want %q", tc.name, snap.Status, tc.wantRunStatus)
		}
		if snap.Outcome == nil {
			t.Errorf("run %s: expected terminal outcome", tc.name)
		}
	}
}

func TestRecoverBoundaryDoesNotStartWorkers(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	orch := &mockOrchestrator{}
	merger := &mockMergeService{}

	obj := &domain.Objective{Description: "recover-boundary", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, merger, newTestEventBus(t, database), logger)
	if err := svc.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if orch.startCalled {
		t.Fatal("Recover started coordinator")
	}
	if merger.started {
		t.Fatal("Recover started merge processor")
	}
	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusCompleted {
		t.Fatalf("status = %q, want %q", snap.Status, domain.RunStatusCompleted)
	}
}

// TestRecoverRunsMarksBlockedForWaitingHuman verifies that an active run
// whose execution is waiting_human gets marked blocked during recovery.
func TestRecoverRunsMarksBlockedForWaitingHuman(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	// Setup: objective still executing, but execution is waiting for human approval.
	obj := &domain.Objective{Description: "blocked-recovery", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	exec := &blueprint.Execution{
		ID:          "exec-waiting",
		ObjectiveID: obj.ID,
		Status:      "waiting_human",
	}
	if err := executionStore.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Simulate restart.
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusBlocked {
		t.Errorf("status = %q, want %q", snap.Status, domain.RunStatusBlocked)
	}
	if snap.Blocked == nil {
		t.Fatal("expected Blocked state")
	}
	if snap.Blocked.Kind != "human_approval" {
		t.Errorf("Blocked.Kind = %q, want %q", snap.Blocked.Kind, "human_approval")
	}
}

func TestRecoverRunsMarksBlockedForRecoveryAndRetryResumes(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	attemptStore := db.NewAttemptStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	orch := &mockOrchestrator{}

	obj := &domain.Objective{Description: "recovery-blocked", Status: domain.ObjectiveStatusPartial}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "failed-stream", Description: "needs help"}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	if err := streamStore.UpdateStatus(ctx, stream.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("marking stream failed: %v", err)
	}
	failedExec := &blueprint.Execution{ID: "exec-recovery-failed", ObjectiveID: obj.ID, ParentID: "parent", StreamID: stream.ID, Status: "failed"}
	if err := executionStore.Create(ctx, failedExec); err != nil {
		t.Fatalf("creating failed execution: %v", err)
	}
	if err := streamStore.UpdateExecutionID(ctx, stream.ID, failedExec.ID); err != nil {
		t.Fatalf("linking failed execution: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	blockedAttempt := &domain.Attempt{
		ObjectiveID:   obj.ID,
		ExecutionID:   failedExec.ID,
		StreamID:      stream.ID,
		AttemptNumber: 2,
		MaxAttempts:   2,
		FailureKind:   domain.FailureAgentOutput,
		Action:        domain.RecoveryActionAskHumanThenResume,
		Status:        domain.AttemptStatusExhausted,
		ErrorSummary:  "retry budget exhausted",
	}
	if err := attemptStore.Create(ctx, blockedAttempt); err != nil {
		t.Fatalf("creating blocked attempt: %v", err)
	}

	svc := newTestServiceWithAttempts(t, runStore, attemptStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusBlocked {
		t.Fatalf("status = %q, want %q", snap.Status, domain.RunStatusBlocked)
	}
	if snap.Blocked == nil {
		t.Fatal("expected blocked state")
	}
	if snap.Blocked.Kind != "recovery" {
		t.Fatalf("Blocked.Kind = %q, want recovery", snap.Blocked.Kind)
	}
	if snap.Blocked.StreamID != stream.ID {
		t.Fatalf("Blocked.StreamID = %q, want %q", snap.Blocked.StreamID, stream.ID)
	}
	if snap.Blocked.AttemptID != blockedAttempt.ID {
		t.Fatalf("Blocked.AttemptID = %q, want %q", snap.Blocked.AttemptID, blockedAttempt.ID)
	}

	resumeSnap, err := svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandRetry, StreamID: stream.ID, Guidance: "check the flaky fixture"})
	if err != nil {
		t.Fatalf("Command(retry): %v", err)
	}
	if !orch.retryCalled {
		t.Fatal("expected coordinator retry call")
	}
	if orch.retryID != failedExec.ID {
		t.Fatalf("retry id = %q, want %q", orch.retryID, failedExec.ID)
	}
	if orch.retryGuidance != "check the flaky fixture" {
		t.Fatalf("retry guidance = %q", orch.retryGuidance)
	}
	if resumeSnap.Status != domain.RunStatusActive {
		t.Fatalf("resume status = %q, want %q", resumeSnap.Status, domain.RunStatusActive)
	}
}

// TestRecoverRunsLeavesActiveExecutingObjective verifies that an active run
// whose objective is still executing stays active (coordinator handles resumption).
func TestRecoverRunsLeavesActiveExecutingObjective(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	// Setup: objective executing, execution running — coordinator will resume it.
	obj := &domain.Objective{Description: "active-recovery", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	exec := &blueprint.Execution{
		ID:          "exec-running",
		ObjectiveID: obj.ID,
		Status:      "running",
	}
	if err := executionStore.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Simulate restart.
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusActive {
		t.Errorf("status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
	if snap.Outcome != nil {
		t.Error("expected no terminal outcome for active run")
	}
}

// TestRecoverRunsOrphanedObjective verifies that a run whose objective no longer
// exists in the database is marked failed during recovery.
func TestRecoverRunsOrphanedObjective(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	// Setup: run pointing to a nonexistent objective.
	run := &domain.Run{ObjectiveID: "nonexistent-objective-id"}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Simulate restart.
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, domain.RunStatusFailed)
	}
	if snap.Outcome == nil || snap.Outcome.Status != domain.RunStatusFailed {
		t.Error("expected failed outcome for orphaned run")
	}
}

// TestRecoverRunsFullRestartScenario is an end-to-end test simulating a daemon
// restart with multiple runs in various states. It verifies that snapshots
// reflect the correct post-recovery state.
func TestRecoverRunsFullRestartScenario(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	// --- Pre-restart state ---

	// Run A: objective completed while daemon was down.
	objA := &domain.Objective{Description: "run-A completed", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objA); err != nil {
		t.Fatalf("creating objA: %v", err)
	}
	planA := &domain.Plan{ObjectiveID: objA.ID}
	if err := planStore.Create(ctx, planA); err != nil {
		t.Fatalf("creating planA: %v", err)
	}
	streamA := &domain.Stream{PlanID: planA.ID, Title: "stream-A", Description: "done"}
	if err := streamStore.Create(ctx, streamA); err != nil {
		t.Fatalf("creating streamA: %v", err)
	}
	// Transition through valid states: pending → executing → completed.
	if err := streamStore.UpdateStatus(ctx, streamA.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("updating streamA to executing: %v", err)
	}
	if err := streamStore.UpdateStatus(ctx, streamA.ID, domain.StreamStatusCompleted); err != nil {
		t.Fatalf("updating streamA to completed: %v", err)
	}
	runA := &domain.Run{ObjectiveID: objA.ID}
	if err := runStore.Create(ctx, runA); err != nil {
		t.Fatalf("creating runA: %v", err)
	}

	// Run B: objective still executing, waiting for human approval.
	objB := &domain.Objective{Description: "run-B blocked", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, objB); err != nil {
		t.Fatalf("creating objB: %v", err)
	}
	execB := &blueprint.Execution{
		ID: "exec-B", ObjectiveID: objB.ID, Status: "waiting_human",
	}
	if err := executionStore.Create(ctx, execB); err != nil {
		t.Fatalf("creating execB: %v", err)
	}
	runB := &domain.Run{ObjectiveID: objB.ID}
	if err := runStore.Create(ctx, runB); err != nil {
		t.Fatalf("creating runB: %v", err)
	}

	// Run C: objective still executing, execution running (coordinator resumes).
	objC := &domain.Objective{Description: "run-C active", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, objC); err != nil {
		t.Fatalf("creating objC: %v", err)
	}
	execC := &blueprint.Execution{
		ID: "exec-C", ObjectiveID: objC.ID, Status: "running",
	}
	if err := executionStore.Create(ctx, execC); err != nil {
		t.Fatalf("creating execC: %v", err)
	}
	runC := &domain.Run{ObjectiveID: objC.ID}
	if err := runStore.Create(ctx, runC); err != nil {
		t.Fatalf("creating runC: %v", err)
	}

	// Run D: already completed before restart — should not be touched.
	objD := &domain.Objective{Description: "run-D already done", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objD); err != nil {
		t.Fatalf("creating objD: %v", err)
	}
	runD := &domain.Run{ObjectiveID: objD.ID}
	if err := runStore.Create(ctx, runD); err != nil {
		t.Fatalf("creating runD: %v", err)
	}
	if err := runStore.UpdateStatus(ctx, runD.ID, domain.RunStatusCompleted); err != nil {
		t.Fatalf("marking runD completed: %v", err)
	}

	// --- Simulate restart ---
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer svc.Stop()

	// --- Verify post-recovery snapshots ---

	// Run A: should be completed with outcome and stream state.
	snapA, err := svc.Snapshot(ctx, runA.ID)
	if err != nil {
		t.Fatalf("Snapshot(A): %v", err)
	}
	if snapA.Status != domain.RunStatusCompleted {
		t.Errorf("run A: status = %q, want %q", snapA.Status, domain.RunStatusCompleted)
	}
	if snapA.Outcome == nil || snapA.Outcome.Status != domain.RunStatusCompleted {
		t.Error("run A: expected completed outcome")
	}
	if len(snapA.Streams) != 1 || snapA.Streams[0].Status != domain.StreamStatusCompleted {
		t.Error("run A: expected one completed stream")
	}

	// Run B: should be blocked (human_approval).
	snapB, err := svc.Snapshot(ctx, runB.ID)
	if err != nil {
		t.Fatalf("Snapshot(B): %v", err)
	}
	if snapB.Status != domain.RunStatusBlocked {
		t.Errorf("run B: status = %q, want %q", snapB.Status, domain.RunStatusBlocked)
	}
	if snapB.Blocked == nil || snapB.Blocked.Kind != "human_approval" {
		t.Error("run B: expected human_approval blocked state")
	}

	// Run C: should stay active (coordinator resumes execution).
	snapC, err := svc.Snapshot(ctx, runC.ID)
	if err != nil {
		t.Fatalf("Snapshot(C): %v", err)
	}
	if snapC.Status != domain.RunStatusActive {
		t.Errorf("run C: status = %q, want %q", snapC.Status, domain.RunStatusActive)
	}

	// Run D: already completed — should not have changed.
	snapD, err := svc.Snapshot(ctx, runD.ID)
	if err != nil {
		t.Fatalf("Snapshot(D): %v", err)
	}
	if snapD.Status != domain.RunStatusCompleted {
		t.Errorf("run D: status = %q, want %q (should be unchanged)", snapD.Status, domain.RunStatusCompleted)
	}
}

func TestRetryFailedStreamEndToEnd(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	// 1. Setup: objective with a failed stream.
	obj := &domain.Objective{Description: "e2e retry", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	stream := &domain.Stream{PlanID: plan.ID, Title: "retry-stream", Description: "will fail then retry"}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}

	subExec := &blueprint.Execution{
		ID:          "sub-exec-retry",
		ObjectiveID: obj.ID,
		ParentID:    "parent",
		StreamID:    stream.ID,
		Status:      "failed",
		StepStates: map[string]*blueprint.StepState{
			"agent_step": {
				StepID: "agent_step",
				Status: blueprint.StepStatusFailed,
				Error:  "tests failed: 3 assertions broken",
			},
		},
	}
	if err := executionStore.Create(ctx, subExec); err != nil {
		t.Fatalf("creating sub-execution: %v", err)
	}

	if err := streamStore.UpdateStatus(ctx, stream.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("marking stream failed: %v", err)
	}
	if err := streamStore.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		t.Fatalf("setting stream execution ID: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusPartial); err != nil {
		t.Fatalf("marking run partial: %v", err)
	}

	// 2. Verify snapshot shows retryable failure before retry.
	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("pre-retry Snapshot: %v", err)
	}
	if len(snap.Streams) != 1 {
		t.Fatalf("pre-retry streams = %d, want 1", len(snap.Streams))
	}
	if !snap.Streams[0].Retryable {
		t.Error("pre-retry: stream should be retryable")
	}
	if snap.Streams[0].Error == "" {
		t.Error("pre-retry: stream should have error message")
	}

	// 3. Retry through Command boundary with guidance.
	retrySnap, err := svc.Command(ctx, run.ID, domain.Command{
		Kind:     domain.CommandRetry,
		StreamID: stream.ID,
		Guidance: "fix the broken assertions by checking the expected values",
	})
	if err != nil {
		t.Fatalf("Command(retry): %v", err)
	}

	// 4. Verify coordinator.Retry was called with correct execution ID and guidance.
	if !orch.retryCalled {
		t.Fatal("coordinator.Retry was not called")
	}
	if orch.retryID != subExec.ID {
		t.Errorf("coordinator.Retry id = %q, want %q", orch.retryID, subExec.ID)
	}
	if orch.retryGuidance != "fix the broken assertions by checking the expected values" {
		t.Errorf("coordinator.Retry guidance = %q, want expected value", orch.retryGuidance)
	}

	// 5. Verify run transitioned back to active after retry.
	if retrySnap.Status != domain.RunStatusActive {
		t.Errorf("post-retry status = %q, want %q", retrySnap.Status, domain.RunStatusActive)
	}
}

func TestRunCompletesPartialObjectiveAfterMergeEvent(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)
	lifecycleMgr := lifecycle.New(objectiveStore, planStore, streamStore, agentStore, eventBus, nil, slog.Default())
	svc, err := New(Config{
		Orchestrator:   &mockOrchestrator{},
		MergeProcessor: &mockMergeService{},
		Lifecycle:      lifecycleMgr,
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       eventBus,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obj := &domain.Objective{Description: "partial merge completion", Status: domain.ObjectiveStatusPartial}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "merged stream", Status: domain.StreamStatusMerged}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusPartial}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	if err := svc.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	eventBus.Emit(domain.EventMergeCompleted, obj.ID, stream.ID, "")

	deadline := time.Now().Add(2 * time.Second)
	for {
		snap, err := svc.View(ctx, run.ID)
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		updated, err := objectiveStore.Get(ctx, obj.ID)
		if err != nil {
			t.Fatalf("Get objective: %v", err)
		}
		if snap.Status == domain.RunStatusCompleted && updated.Status == domain.ObjectiveStatusCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status after merge event: run=%s objective=%s, want completed/completed", snap.Status, updated.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunDoesNotCompletePartialObjectiveUntilEveryStreamMerged(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)
	lifecycleMgr := lifecycle.New(objectiveStore, planStore, streamStore, agentStore, eventBus, nil, slog.Default())
	svc, err := New(Config{
		Orchestrator:   &mockOrchestrator{},
		MergeProcessor: &mockMergeService{},
		Lifecycle:      lifecycleMgr,
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       eventBus,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obj := &domain.Objective{Description: "partial merge waits", Status: domain.ObjectiveStatusPartial}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	mergedStream := &domain.Stream{PlanID: plan.ID, Title: "merged stream", Status: domain.StreamStatusMerged}
	if err := streamStore.Create(ctx, mergedStream); err != nil {
		t.Fatalf("creating merged stream: %v", err)
	}
	completedStream := &domain.Stream{PlanID: plan.ID, Title: "completed stream", Status: domain.StreamStatusCompleted}
	if err := streamStore.Create(ctx, completedStream); err != nil {
		t.Fatalf("creating completed stream: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusPartial}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	svc.completeMerge(ctx, obj.ID)

	snap, err := svc.View(ctx, run.ID)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if snap.Status != domain.RunStatusPartial {
		t.Fatalf("run status = %s, want %s", snap.Status, domain.RunStatusPartial)
	}
	updated, err := objectiveStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if updated.Status != domain.ObjectiveStatusPartial {
		t.Fatalf("objective status = %s, want %s", updated.Status, domain.ObjectiveStatusPartial)
	}
}

func TestRunRecoversCompletedPostMergePartial(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)
	lifecycleMgr := lifecycle.New(objectiveStore, planStore, streamStore, agentStore, eventBus, nil, slog.Default())
	svc, err := New(Config{
		Orchestrator:   &mockOrchestrator{},
		MergeProcessor: &mockMergeService{},
		Lifecycle:      lifecycleMgr,
		Runs:           runStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		Executions:     executionStore,
		Agents:         agentStore,
		EventBus:       eventBus,
		Logger:         slog.Default(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obj := &domain.Objective{Description: "post-merge restart", Status: domain.ObjectiveStatusPartial}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "merged before restart", Status: domain.StreamStatusMerged}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusPartial}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	if err := svc.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := svc.View(ctx, run.ID)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if snap.Status != domain.RunStatusCompleted {
		t.Fatalf("run status = %s, want %s", snap.Status, domain.RunStatusCompleted)
	}
	updated, err := objectiveStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if updated.Status != domain.ObjectiveStatusCompleted {
		t.Fatalf("objective status = %s, want %s", updated.Status, domain.ObjectiveStatusCompleted)
	}
}
