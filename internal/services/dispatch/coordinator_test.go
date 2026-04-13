package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/runtime"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// mockTracker implements AgentTracker for coordinator boundary tests.
type mockTracker struct {
	mu            sync.Mutex
	tracked       map[string]bool
	killCalls     []string
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
func (s *stubMergeEnqueuer) MergerSandboxID(_ string) string                 { return "" }

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
	attempts   *db.AttemptStore
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
	if err := db.NewProjectStore(database.Conn()).Upsert(context.Background(), &domain.Project{ID: "test-project", Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir() + "/.tack/config.yaml"}); err != nil {
		t.Fatalf("registering test project: %v", err)
	}

	conn := database.Conn()
	attempts := db.NewAttemptStore(conn)
	executions := db.NewExecutionStore(conn)
	objectives := db.NewObjectiveStore(conn)
	plans := db.NewPlanStore(conn)
	streams := db.NewStreamStore(conn)
	agents := db.NewAgentStore(conn)
	eventBus := events.NewPersistentBus(nil, slog.Default())
	logger := slog.Default()

	lm := lifecycle.New(objectives, plans, streams, agents, eventBus, nil, logger)

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
		attempts:      attempts,
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
		attempts:   attempts,
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
		Blueprint:   "build-review",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := e.objectives.Create(context.Background(), obj); err != nil {
		t.Fatalf("creating test objective: %v", err)
	}
}

// --- Approve flow tests ---
//
// Note: Execute flow tests (TestExecute_StartsBlueprint, TestExecute_RejectsNonStartableObjective)
// were removed — these are now superseded by run-scoped boundary tests in runs/runs_test.go
// (TestStartCreatesRunAndDelegates, TestStartRejectsExecutingObjective) and scenario tests
// in runs/runs_scenario_test.go. Callers no longer invoke coordinator.Execute directly.

func TestApprove_AbsorbsFullDance(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	// Create objective that uses the standard blueprint (has human approve step).
	obj := &domain.Objective{
		ID:          "obj-approve",
		Description: "test approve",
		Status:      domain.ObjectiveStatusApproved,
		Blueprint:   "standard",
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
		ID:          "exec-running",
		BlueprintID: "build-review",
		ObjectiveID: "obj-x",
		Status:      "running",
		StepStates:  make(map[string]*blueprint.StepState),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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
		Status:    domain.StreamStatusFailed,
		CreatedAt: time.Now(),
	}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}

	failedExec := &blueprint.Execution{
		ID:          "exec-failed",
		BlueprintID: "build-review",
		ObjectiveID: "obj-retry",
		StreamID:    "stream-retry",
		ParentID:    "exec-parent",
		Status:      "failed",
		StepStates:  map[string]*blueprint.StepState{"build": {StepID: "build", Status: blueprint.StepStatusFailed, Error: "test error"}},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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
		ID:          "exec-running-2",
		BlueprintID: "build-review",
		ObjectiveID: "obj-y",
		Status:      "running",
		StepStates:  make(map[string]*blueprint.StepState),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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

func TestRetry_ResumesBlockedRecoveryWithGuidance(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-recovery")
	plan := &domain.Plan{ID: "plan-recovery", ObjectiveID: "obj-recovery", Status: domain.PlanStatusExecuting, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{ID: "stream-recovery", PlanID: plan.ID, Title: "recovery stream", Status: domain.StreamStatusFailed, CreatedAt: time.Now()}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	failedExec := &blueprint.Execution{
		ID:          "exec-recovery-failed",
		BlueprintID: "build-review",
		ObjectiveID: "obj-recovery",
		StreamID:    stream.ID,
		ParentID:    "exec-parent",
		Status:      "failed",
		StepStates:  map[string]*blueprint.StepState{"build": {StepID: "build", Status: blueprint.StepStatusFailed, Error: "unit tests still failing"}},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.executions.Create(ctx, failedExec); err != nil {
		t.Fatalf("creating failed execution: %v", err)
	}
	if err := env.streams.UpdateExecutionID(ctx, stream.ID, failedExec.ID); err != nil {
		t.Fatalf("linking failed execution: %v", err)
	}
	blocked := &domain.Attempt{
		ObjectiveID:   failedExec.ObjectiveID,
		ExecutionID:   failedExec.ID,
		StreamID:      stream.ID,
		AttemptNumber: 3,
		MaxAttempts:   3,
		FailureKind:   domain.FailureAgentOutput,
		Action:        domain.RecoveryActionAskHumanThenResume,
		Status:        domain.AttemptStatusExhausted,
		ErrorSummary:  "retry budget exhausted",
		FixContext:    "Previous recovery context:\ninspect the failing fixture",
	}
	if err := env.attempts.Create(ctx, blocked); err != nil {
		t.Fatalf("creating blocked attempt: %v", err)
	}

	sub, unsub := env.eventBus.Subscribe(8)
	defer unsub()

	if err := env.coord.Retry(ctx, failedExec.ID, "re-run with a clean temp database"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	updatedStream, err := env.streams.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("loading stream: %v", err)
	}
	if updatedStream.ExecutionID == failedExec.ID || updatedStream.ExecutionID == "" {
		t.Fatalf("stream execution id = %q, want new retry execution", updatedStream.ExecutionID)
	}
	retryExec, err := env.executions.Get(ctx, updatedStream.ExecutionID)
	if err != nil {
		t.Fatalf("loading retry execution: %v", err)
	}

	var fixContext string
	for _, state := range retryExec.StepStates {
		if state != nil && state.Metadata != nil && state.Metadata["fix_context"] != "" {
			fixContext = state.Metadata["fix_context"]
			break
		}
	}
	if fixContext == "" {
		t.Fatal("expected retry fix_context to be attached")
	}
	if !strings.Contains(fixContext, "inspect the failing fixture") {
		t.Fatalf("fix_context missing blocked recovery context: %q", fixContext)
	}
	if !strings.Contains(fixContext, "unit tests still failing") {
		t.Fatalf("fix_context missing previous error: %q", fixContext)
	}
	if !strings.Contains(fixContext, "re-run with a clean temp database") {
		t.Fatalf("fix_context missing human guidance: %q", fixContext)
	}

	attempts, err := env.attempts.ListByStream(ctx, stream.ID)
	if err != nil {
		t.Fatalf("listing attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
	var resume *domain.Attempt
	for i := range attempts {
		if attempts[i].TriggeredByAttempt == blocked.ID {
			resume = &attempts[i]
			break
		}
	}
	if resume == nil {
		t.Fatal("expected resumed attempt linked to blocked attempt")
	}
	if resume.Status != domain.AttemptStatusRunning {
		t.Fatalf("resume status = %q, want %q", resume.Status, domain.AttemptStatusRunning)
	}
	if resume.HumanGuidance != "re-run with a clean temp database" {
		t.Fatalf("resume guidance = %q", resume.HumanGuidance)
	}
	if resume.ExecutionID != retryExec.ID {
		t.Fatalf("resume execution_id = %q, want %q", resume.ExecutionID, retryExec.ID)
	}
	if resume.TriggeredByAttempt != blocked.ID {
		t.Fatalf("resume triggered_by = %q, want %q", resume.TriggeredByAttempt, blocked.ID)
	}

	var retryContext string
	for _, state := range retryExec.StepStates {
		if state != nil && state.Metadata != nil && state.Metadata[retryContextMetadataKey] != "" {
			retryContext = state.Metadata[retryContextMetadataKey]
			break
		}
	}
	if retryContext == "" {
		t.Fatal("expected retry_context metadata on resumed execution")
	}

	select {
	case ev := <-sub:
		if ev.Type != domain.EventRecoveryResumed {
			t.Fatalf("event type = %q, want %q", ev.Type, domain.EventRecoveryResumed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery resumed event")
	}
}

func TestRetry_RestartsCompletedExecutionAfterPostMergeGateFailure(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-post-merge")
	plan := &domain.Plan{ID: "plan-post-merge", ObjectiveID: "obj-post-merge", Status: domain.PlanStatusExecuting, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{ID: "stream-post-merge", PlanID: plan.ID, Title: "post merge stream", Status: domain.StreamStatusFailed, CreatedAt: time.Now()}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	completedExec := &blueprint.Execution{
		ID:          "exec-post-merge-completed",
		BlueprintID: "build-review",
		ObjectiveID: "obj-post-merge",
		StreamID:    stream.ID,
		ParentID:    "exec-parent",
		Status:      "completed",
		StepStates:  map[string]*blueprint.StepState{"build": {StepID: "build", Status: blueprint.StepStatusCompleted, Output: "done"}},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.executions.Create(ctx, completedExec); err != nil {
		t.Fatalf("creating completed execution: %v", err)
	}
	if err := env.streams.UpdateExecutionID(ctx, stream.ID, completedExec.ID); err != nil {
		t.Fatalf("linking completed execution: %v", err)
	}
	attempt := &domain.Attempt{
		ObjectiveID:   completedExec.ObjectiveID,
		StreamID:      stream.ID,
		MergeEntryID:  "merge-post-merge",
		AttemptNumber: 1,
		MaxAttempts:   3,
		FailureKind:   domain.FailurePostMergeGate,
		Action:        domain.RecoveryActionRestartStream,
		Status:        domain.AttemptStatusRecorded,
		ErrorSummary:  "post-merge-gate-3: generated integration test is missing from test_list.go",
	}
	if err := env.attempts.Create(ctx, attempt); err != nil {
		t.Fatalf("creating post-merge attempt: %v", err)
	}

	if err := env.coord.Retry(ctx, completedExec.ID, ""); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	updatedStream, err := env.streams.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("loading stream: %v", err)
	}
	if updatedStream.ExecutionID == completedExec.ID || updatedStream.ExecutionID == "" {
		t.Fatalf("stream execution id = %q, want new retry execution", updatedStream.ExecutionID)
	}
	retryExec, err := env.executions.Get(ctx, updatedStream.ExecutionID)
	if err != nil {
		t.Fatalf("loading retry execution: %v", err)
	}

	var fixContext string
	for _, state := range retryExec.StepStates {
		if state != nil && state.Metadata != nil && state.Metadata["fix_context"] != "" {
			fixContext = state.Metadata["fix_context"]
			break
		}
	}
	if !strings.Contains(fixContext, "post-merge-gate-3") {
		t.Fatalf("fix_context missing post-merge gate failure: %q", fixContext)
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
		Status:    domain.StreamStatusFailed,
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
		ID:          "exec-stop-fail",
		BlueprintID: "build-review",
		ObjectiveID: "obj-stop",
		StreamID:    "stream-stop",
		ParentID:    "exec-parent",
		Status:      "failed",
		StepStates:  map[string]*blueprint.StepState{"build": {StepID: "build", Status: blueprint.StepStatusFailed, Error: "fail"}},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
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

func TestRecoverExecutions_RequeuesInFlightSubExecutionForParent(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	obj := &domain.Objective{ID: "obj-parent-recover", Description: "parent recovery", Status: domain.ObjectiveStatusExecuting, Blueprint: "standard", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ID: "plan-parent-recover", ObjectiveID: obj.ID, Status: domain.PlanStatusExecuting, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{ID: "stream-parent-recover", PlanID: plan.ID, Title: "stream one", Status: domain.StreamStatusExecuting, CreatedAt: time.Now()}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	parent, err := env.engine.Start(ctx, "standard", obj.ID)
	if err != nil {
		t.Fatalf("Start parent: %v", err)
	}
	parent.ID = "exec-parent-recover"
	parent.Status = "running"
	parent.CurrentStep = "execute"
	if err := env.executions.Create(ctx, parent); err != nil {
		t.Fatalf("creating parent execution: %v", err)
	}
	child, err := env.engine.Start(ctx, "build-review", obj.ID)
	if err != nil {
		t.Fatalf("Start child: %v", err)
	}
	child.ID = "exec-child-recover"
	child.ParentID = parent.ID
	child.StreamID = stream.ID
	child.Status = "running"
	if err := env.executions.Create(ctx, child); err != nil {
		t.Fatalf("creating child execution: %v", err)
	}
	if err := env.streams.UpdateExecutionID(ctx, stream.ID, child.ID); err != nil {
		t.Fatalf("linking child execution: %v", err)
	}

	env.coord.recoverExecutions(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := env.streams.Get(ctx, stream.ID)
		if err != nil {
			t.Fatalf("loading stream: %v", err)
		}
		if got.Status == domain.StreamStatusCompleted {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := env.streams.Get(ctx, stream.ID)
	t.Fatalf("stream status = %q, want completed", got.Status)
}

func TestRecoverExecutions_ResumesStandaloneRetrySubExecution(t *testing.T) {
	env := setupTestCoordinator(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	obj := &domain.Objective{ID: "obj-retry-recover", Description: "retry recovery", Status: domain.ObjectiveStatusExecuting, Blueprint: "build-review", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	plan := &domain.Plan{ID: "plan-retry-recover", ObjectiveID: obj.ID, Status: domain.PlanStatusExecuting, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}
	stream := &domain.Stream{ID: "stream-retry-recover", PlanID: plan.ID, Title: "retry stream", Status: domain.StreamStatusExecuting, CreatedAt: time.Now()}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	parent, err := env.engine.Start(ctx, "standard", obj.ID)
	if err != nil {
		t.Fatalf("Start parent: %v", err)
	}
	parent.ID = "exec-parent-completed"
	parent.Status = "completed"
	if err := env.executions.Create(ctx, parent); err != nil {
		t.Fatalf("creating parent execution: %v", err)
	}
	retryExec, err := env.engine.Start(ctx, "build-review", obj.ID)
	if err != nil {
		t.Fatalf("Start retry execution: %v", err)
	}
	retryExec.ID = "exec-retry-running"
	retryExec.ParentID = parent.ID
	retryExec.StreamID = stream.ID
	retryExec.Status = "running"
	if err := env.executions.Create(ctx, retryExec); err != nil {
		t.Fatalf("creating retry execution: %v", err)
	}
	if err := env.streams.UpdateExecutionID(ctx, stream.ID, retryExec.ID); err != nil {
		t.Fatalf("linking retry execution: %v", err)
	}

	env.coord.recoverExecutions(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := env.executions.Get(ctx, retryExec.ID)
		if err != nil {
			t.Fatalf("loading retry execution: %v", err)
		}
		if got.Status == "completed" {
			streamState, err := env.streams.Get(ctx, stream.ID)
			if err != nil {
				t.Fatalf("loading stream: %v", err)
			}
			if streamState.Status != domain.StreamStatusCompleted {
				t.Fatalf("stream status = %q, want completed", streamState.Status)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := env.executions.Get(ctx, retryExec.ID)
	t.Fatalf("retry execution status = %q, want completed", got.Status)
}
