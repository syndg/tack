package runs

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/events"
)

// mockOrchestrator is a minimal test double for dispatch.Orchestrator.
type mockOrchestrator struct {
	executeCalled bool
	executeID     string
	executeErr    error

	approveCalled bool
	approveID     string
	approveErr    error

	retryCalled   bool
	retryID       string
	retryGuidance string
	retryErr      error

	stopCalled bool
}

func (m *mockOrchestrator) Start(ctx context.Context) error { return nil }
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
func (m *mockOrchestrator) Kill(ctx context.Context, sessionID string) error { return nil }
func (m *mockOrchestrator) Execute(ctx context.Context, objectiveID string) error {
	m.executeCalled = true
	m.executeID = objectiveID
	return m.executeErr
}

// mockMergeService is a minimal test double for MergeService.
type mockMergeService struct {
	started bool
	stopped bool
}

func (m *mockMergeService) Start(ctx context.Context) error { m.started = true; return nil }
func (m *mockMergeService) Stop()                           { m.stopped = true }

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "will fail", Status: domain.ObjectiveStatusApproved}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	_, err := svc.Start(ctx, obj.ID)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, merger, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, eventBus, logger)

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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)

	orch := &mockOrchestrator{}
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

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
		Kind:     domain.CommandRetry,
		StreamID: stream.ID,
		Guidance: "try a different approach",
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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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

func TestCommandAbortStopsAndFailsRun(t *testing.T) {
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

	obj := &domain.Objective{Description: "abort test", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	snap, err := svc.Command(ctx, run.ID, domain.Command{
		Kind:   domain.CommandAbort,
		Reason: "test abort",
	})
	if err != nil {
		t.Fatalf("Command(abort): %v", err)
	}

	// Verify coordinator.Stop was called.
	if !orch.stopCalled {
		t.Error("coordinator.Stop was not called")
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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)

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
		name         string
		objStatus    domain.ObjectiveStatus
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, &mockOrchestrator{}, &mockMergeService{}, newTestEventBus(t, database), logger)
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
	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, newTestEventBus(t, database), logger)

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
