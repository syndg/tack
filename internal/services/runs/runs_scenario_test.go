package runs

// Scenario-driven boundary tests for the run-centric orchestration API.
//
// These tests validate complete user-facing workflows through the Runs boundary:
// start, block/approve, fail/retry, abort, and restart recovery. They assert
// behavior through Snapshots and persisted state rather than internal helper
// call choreography, superseding shallow dispatch seam tests.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/events"
)

// scenarioEnv bundles all stores and services needed for scenario tests.
type scenarioEnv struct {
	svc        *Service
	orch       *mockOrchestrator
	eventBus   *events.PersistentBus
	runs       *db.RunStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	executions *db.ExecutionStore
	agents     *db.AgentStore
}

func setupScenario(t *testing.T) *scenarioEnv {
	t.Helper()
	database := openTestDB(t)
	conn := database.Conn()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)
	agentStore := db.NewAgentStore(conn)
	eventBus := newTestEventBus(t, database)

	orch := &mockOrchestrator{}
	svc := newTestService(t, runStore, objectiveStore, planStore, streamStore, executionStore, agentStore, orch, &mockMergeService{}, eventBus, slog.Default())

	return &scenarioEnv{
		svc:        svc,
		orch:       orch,
		eventBus:   eventBus,
		runs:       runStore,
		objectives: objectiveStore,
		plans:      planStore,
		streams:    streamStore,
		executions: executionStore,
		agents:     agentStore,
	}
}

// TestScenario_StartBlockApproveComplete walks the full lifecycle:
//
//	daemon restart with waiting_human execution → recovery marks run blocked →
//	snapshot shows blocked state → approve unblocks → objective completes →
//	event sync marks run completed → snapshot shows terminal outcome.
func TestScenario_StartBlockApproveComplete(t *testing.T) {
	env := setupScenario(t)
	ctx := context.Background()

	// --- Phase 1: pre-restart state ---
	// Objective is executing, execution is waiting for human approval.
	obj := &domain.Objective{Description: "block-approve scenario", Status: domain.ObjectiveStatusExecuting}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	stream := &domain.Stream{PlanID: plan.ID, Title: "feature-stream", Description: "the work"}
	if err := env.streams.Create(ctx, stream); err != nil {
		t.Fatalf("creating stream: %v", err)
	}
	if err := env.streams.UpdateStatus(ctx, stream.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("marking stream executing: %v", err)
	}

	exec := &blueprint.Execution{
		ID:          "exec-blocked",
		ObjectiveID: obj.ID,
		Status:      "waiting_human",
	}
	if err := env.executions.Create(ctx, exec); err != nil {
		t.Fatalf("creating execution: %v", err)
	}

	run := &domain.Run{ObjectiveID: obj.ID}
	if err := env.runs.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// --- Phase 2: daemon restart triggers recovery ---
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := env.svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer env.svc.Stop()

	// Snapshot should show blocked state after recovery.
	snap, err := env.svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("post-recovery Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusBlocked {
		t.Errorf("post-recovery: status = %q, want %q", snap.Status, domain.RunStatusBlocked)
	}
	if snap.Blocked == nil {
		t.Fatal("post-recovery: expected Blocked state")
	}
	if snap.Blocked.Kind != "human_approval" {
		t.Errorf("post-recovery: Blocked.Kind = %q, want %q", snap.Blocked.Kind, "human_approval")
	}
	if snap.Outcome != nil {
		t.Error("post-recovery: should not have terminal outcome yet")
	}
	// Stream should be visible in snapshot.
	if len(snap.Streams) != 1 {
		t.Fatalf("post-recovery: len(Streams) = %d, want 1", len(snap.Streams))
	}
	if snap.Streams[0].Title != "feature-stream" {
		t.Errorf("post-recovery: stream title = %q, want %q", snap.Streams[0].Title, "feature-stream")
	}

	// --- Phase 3: approve the human gate ---
	approveSnap, err := env.svc.Command(ctx, run.ID, domain.Command{Kind: domain.CommandApprove})
	if err != nil {
		t.Fatalf("Command(approve): %v", err)
	}
	if !env.orch.approveCalled {
		t.Error("coordinator.Approve was not called")
	}
	if env.orch.approveID != exec.ID {
		t.Errorf("coordinator.Approve id = %q, want %q", env.orch.approveID, exec.ID)
	}
	if approveSnap.Status != domain.RunStatusActive {
		t.Errorf("post-approve: status = %q, want %q", approveSnap.Status, domain.RunStatusActive)
	}
	if approveSnap.Blocked != nil {
		t.Error("post-approve: should not be blocked")
	}

	// --- Phase 4: objective completes via event ---
	env.eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusCompleted),
	)
	time.Sleep(50 * time.Millisecond)

	// Final snapshot should show completed with outcome.
	finalSnap, err := env.svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("final Snapshot: %v", err)
	}
	if finalSnap.Status != domain.RunStatusCompleted {
		t.Errorf("final: status = %q, want %q", finalSnap.Status, domain.RunStatusCompleted)
	}
	if finalSnap.Outcome == nil {
		t.Fatal("final: expected terminal outcome")
	}
	if finalSnap.Outcome.Status != domain.RunStatusCompleted {
		t.Errorf("final: outcome status = %q, want %q", finalSnap.Outcome.Status, domain.RunStatusCompleted)
	}
}

// TestScenario_StartFailRetryComplete walks the fail/retry lifecycle:
//
//	start objective → stream fails → event marks run partial →
//	snapshot shows retryable failure with error → retry with guidance →
//	run becomes active → objective completes → snapshot shows completed.
func TestScenario_StartFailRetryComplete(t *testing.T) {
	env := setupScenario(t)
	ctx := context.Background()

	// --- Phase 1: start the objective ---
	obj := &domain.Objective{Description: "fail-retry scenario", Status: domain.ObjectiveStatusApproved}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	streamOK := &domain.Stream{PlanID: plan.ID, Title: "api-stream", Description: "API changes"}
	if err := env.streams.Create(ctx, streamOK); err != nil {
		t.Fatalf("creating ok stream: %v", err)
	}

	streamFail := &domain.Stream{PlanID: plan.ID, Title: "db-stream", Description: "DB migration"}
	if err := env.streams.Create(ctx, streamFail); err != nil {
		t.Fatalf("creating fail stream: %v", err)
	}

	// Start through the boundary.
	startSnap, err := env.svc.Start(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if startSnap.Status != domain.RunStatusActive {
		t.Errorf("start: status = %q, want %q", startSnap.Status, domain.RunStatusActive)
	}
	if !env.orch.executeCalled {
		t.Fatal("coordinator.Execute was not called")
	}
	runID := startSnap.RunID

	// Start the event loop so status sync works.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := env.svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer env.svc.Stop()

	// --- Phase 2: simulate one stream completing, one failing ---
	// Complete the ok stream.
	if err := env.streams.UpdateStatus(ctx, streamOK.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("executing ok stream: %v", err)
	}
	if err := env.streams.UpdateStatus(ctx, streamOK.ID, domain.StreamStatusCompleted); err != nil {
		t.Fatalf("completing ok stream: %v", err)
	}

	// Fail the db stream with a sub-execution error.
	subExec := &blueprint.Execution{
		ID:          "sub-exec-db",
		ObjectiveID: obj.ID,
		ParentID:    "parent-exec",
		StreamID:    streamFail.ID,
		Status:      "failed",
		StepStates: map[string]*blueprint.StepState{
			"migrate_step": {
				StepID: "migrate_step",
				Status: blueprint.StepStatusFailed,
				Error:  "migration failed: column already exists",
			},
		},
	}
	if err := env.executions.Create(ctx, subExec); err != nil {
		t.Fatalf("creating failed sub-execution: %v", err)
	}
	if err := env.streams.UpdateStatus(ctx, streamFail.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("failing db stream: %v", err)
	}
	if err := env.streams.UpdateExecutionID(ctx, streamFail.ID, subExec.ID); err != nil {
		t.Fatalf("linking stream to execution: %v", err)
	}

	// Objective transitions to partial (some streams failed).
	env.eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusPartial),
	)
	time.Sleep(50 * time.Millisecond)

	// --- Phase 3: inspect snapshot — should show partial with retryable failure ---
	partialSnap, err := env.svc.Snapshot(ctx, runID)
	if err != nil {
		t.Fatalf("partial Snapshot: %v", err)
	}
	if partialSnap.Status != domain.RunStatusPartial {
		t.Errorf("partial: status = %q, want %q", partialSnap.Status, domain.RunStatusPartial)
	}
	if partialSnap.Outcome == nil || partialSnap.Outcome.Status != domain.RunStatusPartial {
		t.Error("partial: expected partial outcome")
	}
	if len(partialSnap.Streams) != 2 {
		t.Fatalf("partial: len(Streams) = %d, want 2", len(partialSnap.Streams))
	}

	// Find the failed stream and verify retryable info.
	var failedStream *domain.RunStreamState
	var okStream *domain.RunStreamState
	for i := range partialSnap.Streams {
		switch partialSnap.Streams[i].Title {
		case "db-stream":
			failedStream = &partialSnap.Streams[i]
		case "api-stream":
			okStream = &partialSnap.Streams[i]
		}
	}

	if failedStream == nil {
		t.Fatal("partial: failed stream not found")
	}
	if failedStream.Status != domain.StreamStatusFailed {
		t.Errorf("partial: failed stream status = %q, want %q", failedStream.Status, domain.StreamStatusFailed)
	}
	if !failedStream.Retryable {
		t.Error("partial: failed stream should be retryable")
	}
	if failedStream.Error != "migration failed: column already exists" {
		t.Errorf("partial: failed stream error = %q, want %q", failedStream.Error, "migration failed: column already exists")
	}
	if okStream == nil || okStream.Status != domain.StreamStatusCompleted {
		t.Error("partial: ok stream should be completed")
	}
	if okStream.Retryable {
		t.Error("partial: ok stream should not be retryable")
	}

	// --- Phase 4: retry the failed stream with guidance ---
	retrySnap, err := env.svc.Command(ctx, runID, domain.Command{
		Kind:     domain.CommandRetry,
		StreamID: streamFail.ID,
		Guidance: "use IF NOT EXISTS on the ALTER TABLE statement",
	})
	if err != nil {
		t.Fatalf("Command(retry): %v", err)
	}
	if !env.orch.retryCalled {
		t.Fatal("coordinator.Retry was not called")
	}
	if env.orch.retryID != subExec.ID {
		t.Errorf("coordinator.Retry id = %q, want %q", env.orch.retryID, subExec.ID)
	}
	if env.orch.retryGuidance != "use IF NOT EXISTS on the ALTER TABLE statement" {
		t.Errorf("coordinator.Retry guidance = %q, want expected", env.orch.retryGuidance)
	}
	if retrySnap.Status != domain.RunStatusActive {
		t.Errorf("post-retry: status = %q, want %q", retrySnap.Status, domain.RunStatusActive)
	}

	// --- Phase 5: objective completes after retry succeeds ---
	env.eventBus.Emit(domain.EventObjectiveUpdated, obj.ID, "", "",
		"from", string(domain.ObjectiveStatusPartial),
		"to", string(domain.ObjectiveStatusCompleted),
	)
	time.Sleep(50 * time.Millisecond)

	finalSnap, err := env.svc.Snapshot(ctx, runID)
	if err != nil {
		t.Fatalf("final Snapshot: %v", err)
	}
	if finalSnap.Status != domain.RunStatusCompleted {
		t.Errorf("final: status = %q, want %q", finalSnap.Status, domain.RunStatusCompleted)
	}
	if finalSnap.Outcome == nil || finalSnap.Outcome.Status != domain.RunStatusCompleted {
		t.Error("final: expected completed outcome")
	}
}

// TestScenario_StartAbortTerminal walks the abort lifecycle:
//
//	start objective → abort while active → coordinator stopped →
//	snapshot shows failed with outcome → further commands rejected.
func TestScenario_StartAbortTerminal(t *testing.T) {
	env := setupScenario(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "abort scenario", Status: domain.ObjectiveStatusApproved}
	if err := env.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	// Start through the boundary.
	startSnap, err := env.svc.Start(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	runID := startSnap.RunID

	// Abort the run.
	abortSnap, err := env.svc.Command(ctx, runID, domain.Command{
		Kind:   domain.CommandAbort,
		Reason: "requirements changed, cancelling",
	})
	if err != nil {
		t.Fatalf("Command(abort): %v", err)
	}
	if !env.orch.stopCalled {
		t.Error("coordinator.Stop was not called")
	}
	if abortSnap.Status != domain.RunStatusFailed {
		t.Errorf("post-abort: status = %q, want %q", abortSnap.Status, domain.RunStatusFailed)
	}
	if abortSnap.Outcome == nil || abortSnap.Outcome.Status != domain.RunStatusFailed {
		t.Error("post-abort: expected failed outcome")
	}

	// Further commands should be rejected on the terminal run.
	_, err = env.svc.Command(ctx, runID, domain.Command{Kind: domain.CommandAbort})
	if err == nil {
		t.Fatal("expected error aborting already-terminal run")
	}

	_, err = env.svc.Command(ctx, runID, domain.Command{Kind: domain.CommandApprove})
	if err == nil {
		t.Fatal("expected error approving terminal run")
	}
}

// TestScenario_RestartRecoveryMultiRun verifies that daemon restart correctly
// reconciles multiple runs in different states, then allows continued
// interaction through the boundary.
func TestScenario_RestartRecoveryMultiRun(t *testing.T) {
	env := setupScenario(t)
	ctx := context.Background()

	// --- Pre-restart state: three runs ---

	// Run 1: objective completed while daemon was down.
	obj1 := &domain.Objective{Description: "completed-obj", Status: domain.ObjectiveStatusCompleted}
	if err := env.objectives.Create(ctx, obj1); err != nil {
		t.Fatalf("creating obj1: %v", err)
	}
	plan1 := &domain.Plan{ObjectiveID: obj1.ID}
	if err := env.plans.Create(ctx, plan1); err != nil {
		t.Fatalf("creating plan1: %v", err)
	}
	stream1 := &domain.Stream{PlanID: plan1.ID, Title: "done-stream", Description: "done"}
	if err := env.streams.Create(ctx, stream1); err != nil {
		t.Fatalf("creating stream1: %v", err)
	}
	if err := env.streams.UpdateStatus(ctx, stream1.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("executing stream1: %v", err)
	}
	if err := env.streams.UpdateStatus(ctx, stream1.ID, domain.StreamStatusCompleted); err != nil {
		t.Fatalf("completing stream1: %v", err)
	}
	run1 := &domain.Run{ObjectiveID: obj1.ID}
	if err := env.runs.Create(ctx, run1); err != nil {
		t.Fatalf("creating run1: %v", err)
	}

	// Run 2: objective executing, execution waiting for human approval.
	obj2 := &domain.Objective{Description: "blocked-obj", Status: domain.ObjectiveStatusExecuting}
	if err := env.objectives.Create(ctx, obj2); err != nil {
		t.Fatalf("creating obj2: %v", err)
	}
	exec2 := &blueprint.Execution{
		ID:          "exec-waiting-2",
		ObjectiveID: obj2.ID,
		Status:      "waiting_human",
	}
	if err := env.executions.Create(ctx, exec2); err != nil {
		t.Fatalf("creating exec2: %v", err)
	}
	run2 := &domain.Run{ObjectiveID: obj2.ID}
	if err := env.runs.Create(ctx, run2); err != nil {
		t.Fatalf("creating run2: %v", err)
	}

	// Run 3: objective failed while daemon was down.
	obj3 := &domain.Objective{Description: "failed-obj", Status: domain.ObjectiveStatusFailed}
	if err := env.objectives.Create(ctx, obj3); err != nil {
		t.Fatalf("creating obj3: %v", err)
	}
	run3 := &domain.Run{ObjectiveID: obj3.ID}
	if err := env.runs.Create(ctx, run3); err != nil {
		t.Fatalf("creating run3: %v", err)
	}

	// --- Simulate daemon restart ---
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := env.svc.Run(runCtx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer env.svc.Stop()

	// --- Verify recovery snapshots ---

	// Run 1: should be completed.
	snap1, err := env.svc.Snapshot(ctx, run1.ID)
	if err != nil {
		t.Fatalf("Snapshot(run1): %v", err)
	}
	if snap1.Status != domain.RunStatusCompleted {
		t.Errorf("run1: status = %q, want %q", snap1.Status, domain.RunStatusCompleted)
	}
	if snap1.Outcome == nil || snap1.Outcome.Status != domain.RunStatusCompleted {
		t.Error("run1: expected completed outcome")
	}
	if len(snap1.Streams) != 1 || snap1.Streams[0].Status != domain.StreamStatusCompleted {
		t.Error("run1: expected one completed stream")
	}

	// Run 2: should be blocked.
	snap2, err := env.svc.Snapshot(ctx, run2.ID)
	if err != nil {
		t.Fatalf("Snapshot(run2): %v", err)
	}
	if snap2.Status != domain.RunStatusBlocked {
		t.Errorf("run2: status = %q, want %q", snap2.Status, domain.RunStatusBlocked)
	}
	if snap2.Blocked == nil || snap2.Blocked.Kind != "human_approval" {
		t.Error("run2: expected human_approval blocked state")
	}

	// Run 3: should be failed.
	snap3, err := env.svc.Snapshot(ctx, run3.ID)
	if err != nil {
		t.Fatalf("Snapshot(run3): %v", err)
	}
	if snap3.Status != domain.RunStatusFailed {
		t.Errorf("run3: status = %q, want %q", snap3.Status, domain.RunStatusFailed)
	}
	if snap3.Outcome == nil || snap3.Outcome.Status != domain.RunStatusFailed {
		t.Error("run3: expected failed outcome")
	}

	// --- Continue interaction: approve blocked run 2, then it completes ---
	_, err = env.svc.Command(ctx, run2.ID, domain.Command{Kind: domain.CommandApprove})
	if err != nil {
		t.Fatalf("Command(approve) on run2: %v", err)
	}
	if !env.orch.approveCalled {
		t.Error("coordinator.Approve was not called for run2")
	}

	// Simulate run2's objective completing.
	env.eventBus.Emit(domain.EventObjectiveUpdated, obj2.ID, "", "",
		"from", string(domain.ObjectiveStatusExecuting),
		"to", string(domain.ObjectiveStatusCompleted),
	)
	time.Sleep(50 * time.Millisecond)

	snap2Final, err := env.svc.Snapshot(ctx, run2.ID)
	if err != nil {
		t.Fatalf("final Snapshot(run2): %v", err)
	}
	if snap2Final.Status != domain.RunStatusCompleted {
		t.Errorf("run2 final: status = %q, want %q", snap2Final.Status, domain.RunStatusCompleted)
	}
	if snap2Final.Outcome == nil || snap2Final.Outcome.Status != domain.RunStatusCompleted {
		t.Error("run2 final: expected completed outcome")
	}
}
