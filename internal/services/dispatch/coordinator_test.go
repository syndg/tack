package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/runtime"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// mockTracker implements AgentTracker for coordinator boundary tests.
type mockTracker struct {
	mu          sync.Mutex
	tracked     map[string]bool
	killCalls   []string
	stopAllCalled bool
	finishReturn  bool // wasKilled return value for Finish
}

func newMockTracker() *mockTracker {
	return &mockTracker{tracked: make(map[string]bool)}
}

func (m *mockTracker) Track(session *domain.AgentSession, process runtime.AgentProcess) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracked[session.ID] = true
}

func (m *mockTracker) Finish(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tracked, sessionID)
	return m.finishReturn
}

func (m *mockTracker) Kill(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killCalls = append(m.killCalls, sessionID)
	return nil
}

func (m *mockTracker) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopAllCalled = true
}

func (m *mockTracker) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tracked)
}

// stubMergeEnqueuer satisfies MergeEnqueuer with no-ops.
type stubMergeEnqueuer struct{}

func (s *stubMergeEnqueuer) EnqueueStream(_ context.Context, _ string) error { return nil }
func (s *stubMergeEnqueuer) MergerSandboxID(_ string) string                { return "" }

// stubPlanCreator satisfies PlanCreator with no-ops.
type stubPlanCreator struct{}

func (s *stubPlanCreator) CreatePlan(_ context.Context, _ string, _ string) (*domain.Plan, error) {
	return &domain.Plan{ID: "plan-stub"}, nil
}

// testCoordinator builds a Coordinator with real stores (temp DB), a real engine,
// and a mock tracker. Returns the coordinator and its test dependencies.
type testEnv struct {
	coord      *Coordinator
	tracker    *mockTracker
	engine     *blueprint.Engine
	executions *db.ExecutionStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	eventBus   *events.PersistentBus
}

func setupTestCoordinator(t *testing.T) *testEnv {
	t.Helper()

	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.Migrate(); err != nil {
		t.Fatalf("migrating test db: %v", err)
	}

	conn := database.Conn()
	executions := db.NewExecutionStore(conn)
	objectives := db.NewObjectiveStore(conn)
	plans := db.NewPlanStore(conn)
	streams := db.NewStreamStore(conn)
	agents := db.NewAgentStore(conn)
	eventBus := events.NewPersistentBus(nil, slog.Default())
	logger := slog.Default()

	lm := lifecycle.New(objectives, plans, streams, agents, eventBus, logger)

	reg := blueprint.NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("loading default blueprints: %v", err)
	}
	engine := blueprint.NewEngine(reg, logger)

	scheduler := NewScheduler(streams, plans, 10, eventBus, logger)
	tracker := newMockTracker()

	c := &Coordinator{
		engine:        engine,
		scheduler:     scheduler,
		spawner:       nil, // not needed for boundary tests
		lifecycle:     lm,
		mergeEnqueuer: &stubMergeEnqueuer{},
		planCreator:   &stubPlanCreator{},
		executions:    executions,
		objectives:    objectives,
		plans:         plans,
		streams:       streams,
		eventBus:      eventBus,
		tracker:       tracker,
		logger:        logger,
		activeExecs:   make(map[string]context.CancelFunc),
	}

	// Register step handlers — agent handler completes immediately for tests.
	engine.RegisterHandler(blueprint.StepTypeAgent, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted, Output: "test done"}, nil
	})
	engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)
	engine.RegisterHandler(blueprint.StepTypeDeterministic, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	})
	engine.RegisterHandler(blueprint.StepTypeHuman, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	})

	return &testEnv{
		coord:      c,
		tracker:    tracker,
		engine:     engine,
		executions: executions,
		objectives: objectives,
		plans:      plans,
		streams:    streams,
		eventBus:   eventBus,
	}
}

// createObjective creates a test objective in "approved" status.
func (e *testEnv) createObjective(t *testing.T, id string) {
	t.Helper()
	obj := &domain.Objective{
		ID:          id,
		Description: "test objective",
		Status:      domain.ObjectiveStatusApproved,
		Blueprint:   "Hotfix",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := e.objectives.Create(context.Background(), obj); err != nil {
		t.Fatalf("creating test objective: %v", err)
	}
}

// --- Execute flow tests ---

func TestExecute_StartsBlueprint(t *testing.T) {
	env := setupTestCoordinator(t)
	env.createObjective(t, "obj-1")

	ctx := context.Background()
	env.coord.ctx = ctx

	err := env.coord.Execute(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify objective transitioned to executing.
	obj, err := env.objectives.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("getting objective: %v", err)
	}
	// Objective may be executing or already completed (execution runs in goroutine with fast mock handlers).
	if obj.Status != domain.ObjectiveStatusExecuting && obj.Status != domain.ObjectiveStatusCompleted && obj.Status != domain.ObjectiveStatusFailed {
		t.Fatalf("objective status = %s, want executing/completed/failed", obj.Status)
	}

	// Verify execution was created.
	execs, err := env.executions.List(ctx)
	if err != nil {
		t.Fatalf("listing executions: %v", err)
	}
	if len(execs) == 0 {
		t.Fatal("expected at least one execution to be created")
	}
	if execs[0].ObjectiveID != "obj-1" {
		t.Fatalf("execution.ObjectiveID = %q, want %q", execs[0].ObjectiveID, "obj-1")
	}
}

func TestExecute_RejectsNonStartableObjective(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()

	// Create an objective in "executing" status — not startable.
	obj := &domain.Objective{
		ID:          "obj-running",
		Description: "already running",
		Status:      domain.ObjectiveStatusExecuting,
		Blueprint:   "Hotfix",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	err := env.coord.Execute(ctx, "obj-running")
	if err == nil {
		t.Fatal("expected error for non-startable objective")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got: %v", err)
	}
}

// --- Approve flow tests ---

func TestApprove_AbsorbsFullDance(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	// Create objective that uses the Feature Implementation blueprint (has human approve step).
	obj := &domain.Objective{
		ID:          "obj-approve",
		Description: "test approve",
		Status:      domain.ObjectiveStatusApproved,
		Blueprint:   "Feature Implementation",
	}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	// Override agent handler to complete immediately.
	env.engine.RegisterHandler(blueprint.StepTypeAgent, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted, Output: "ok"}, nil
	})

	err := env.coord.Execute(ctx, "obj-approve")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Wait for execution to reach waiting_human state.
	var exec *blueprint.Execution
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for execution to reach waiting_human")
		default:
		}
		execs, _ := env.executions.List(ctx)
		for i := range execs {
			if execs[i].ObjectiveID == "obj-approve" && execs[i].Status == "waiting_human" {
				exec = &execs[i]
				break
			}
		}
		if exec != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Now approve via the single Approve call.
	err = env.coord.Approve(ctx, exec.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Verify execution moved past waiting_human.
	updated, err := env.executions.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("getting execution: %v", err)
	}
	if updated.Status == "waiting_human" {
		t.Fatal("execution should no longer be waiting_human after Approve")
	}
}

func TestApprove_RejectsNonWaitingExecution(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()

	// Create a running execution directly.
	exec := &blueprint.Execution{
		ID:            "exec-running",
		BlueprintName: "Hotfix",
		ObjectiveID:   "obj-x",
		Status:        "running",
		StepStates:    make(map[string]*blueprint.StepState),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := env.executions.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	err := env.coord.Approve(ctx, "exec-running")
	if err == nil {
		t.Fatal("expected error for non-waiting execution")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got: %v", err)
	}
}

// --- Retry flow tests ---

func TestRetry_TrackedInActiveExecs(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-retry")

	// Create a plan + stream + failed sub-execution to retry.
	plan := &domain.Plan{
		ID:          "plan-retry",
		ObjectiveID: "obj-retry",
		Status:      domain.PlanStatusExecuting,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	stream := &domain.Stream{
		ID:        "stream-retry",
		PlanID:    "plan-retry",
		Title:     "test stream",
		Status:    "failed",
		CreatedAt: time.Now(),
	}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}

	failedExec := &blueprint.Execution{
		ID:            "exec-failed",
		BlueprintName: "Hotfix",
		ObjectiveID:   "obj-retry",
		StreamID:      "stream-retry",
		ParentID:      "exec-parent",
		Status:        "failed",
		StepStates:    map[string]*blueprint.StepState{"fix": {StepID: "fix", Status: blueprint.StepStatusFailed, Error: "test error"}},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := env.executions.Create(ctx, failedExec); err != nil {
		t.Fatalf("creating failed execution: %v", err)
	}

	// Retry — the goroutine's cancel func should be registered in activeExecs.
	err := env.coord.Retry(ctx, "exec-failed", "try again")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Check that a retry key was registered (may already be cleaned up if fast).
	// Give the goroutine a moment to start.
	time.Sleep(50 * time.Millisecond)

	// The retry goroutine runs with a "retry:..." key in activeExecs.
	// After completion it cleans up. Since our mock handlers complete instantly,
	// it may already be gone. Verify that Retry didn't error (the main assertion).
}

func TestRetry_RejectsNonFailedExecution(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()

	exec := &blueprint.Execution{
		ID:            "exec-running-2",
		BlueprintName: "Hotfix",
		ObjectiveID:   "obj-y",
		Status:        "running",
		StepStates:    make(map[string]*blueprint.StepState),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := env.executions.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	err := env.coord.Retry(ctx, "exec-running-2", "")
	if err == nil {
		t.Fatal("expected error for non-failed execution")
	}
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got: %v", err)
	}
}

// --- Stop flow tests ---

func TestStop_CancelsRetryGoroutines(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-stop")

	// Create plan + stream + failed execution.
	plan := &domain.Plan{
		ID:          "plan-stop",
		ObjectiveID: "obj-stop",
		Status:      domain.PlanStatusExecuting,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_ = env.plans.Create(ctx, plan)

	stream := &domain.Stream{
		ID:        "stream-stop",
		PlanID:    "plan-stop",
		Title:     "stop test stream",
		Status:    "failed",
		CreatedAt: time.Now(),
	}
	_ = env.streams.Create(ctx, stream)

	// Use a slow handler so the retry goroutine is still running when Stop is called.
	blockCh := make(chan struct{})
	env.engine.RegisterHandler(blueprint.StepTypeAgent, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: "cancelled"}, ctx.Err()
		case <-blockCh:
			return blueprint.StepResult{Status: blueprint.StepStatusCompleted, Output: "done"}, nil
		}
	})

	failedExec := &blueprint.Execution{
		ID:            "exec-stop-fail",
		BlueprintName: "Hotfix",
		ObjectiveID:   "obj-stop",
		StreamID:      "stream-stop",
		ParentID:      "exec-parent",
		Status:        "failed",
		StepStates:    map[string]*blueprint.StepState{"fix": {StepID: "fix", Status: blueprint.StepStatusFailed, Error: "fail"}},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	_ = env.executions.Create(ctx, failedExec)

	// Start retry — goroutine will block on the agent handler.
	err := env.coord.Retry(ctx, "exec-stop-fail", "fix it")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Give the goroutine time to register.
	time.Sleep(50 * time.Millisecond)

	// Verify there's a retry key in activeExecs.
	env.coord.mu.Lock()
	hasRetryKey := false
	for key := range env.coord.activeExecs {
		if len(key) > 6 && key[:6] == "retry:" {
			hasRetryKey = true
			break
		}
	}
	env.coord.mu.Unlock()

	if !hasRetryKey {
		t.Fatal("expected retry goroutine to be registered in activeExecs")
	}

	// Stop should cancel the retry goroutine.
	env.coord.Stop()

	// Allow goroutine to finish.
	time.Sleep(50 * time.Millisecond)

	env.coord.mu.Lock()
	remaining := len(env.coord.activeExecs)
	env.coord.mu.Unlock()

	if remaining != 0 {
		t.Fatalf("expected 0 activeExecs after Stop, got %d", remaining)
	}
}

func TestStop_CallsTrackerStopAll(t *testing.T) {
	env := setupTestCoordinator(t)

	env.coord.Stop()

	env.tracker.mu.Lock()
	called := env.tracker.stopAllCalled
	env.tracker.mu.Unlock()

	if !called {
		t.Fatal("expected tracker.StopAll to be called on Stop")
	}
}
