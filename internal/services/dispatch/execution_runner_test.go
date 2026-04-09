package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
)

func TestHandleBlueprintRefStep_SingleStreamCompletes(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-bp1", domain.ObjectiveStatusApproved)
	env.createPlan(t, "plan-bp1", "obj-bp1", []string{"stream-one"})

	err := env.coord.Execute(ctx, "obj-bp1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Wait for execution to complete (mock handlers complete instantly).
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for execution to complete")
		default:
		}
		obj, _ := env.objectives.Get(ctx, "obj-bp1")
		if obj.Status == domain.ObjectiveStatusCompleted || obj.Status == domain.ObjectiveStatusPartial || obj.Status == domain.ObjectiveStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify stream reached a terminal state.
	streams, _ := env.streams.ListByPlan(ctx, "plan-bp1")
	if len(streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(streams))
	}
}

func TestHandleBlueprintRefStep_MultiStreamCascade(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	env.createObjective(t, "obj-cascade", domain.ObjectiveStatusApproved)

	// Create 2 streams, stream-b depends on stream-a.
	plan := &domain.Plan{
		ID:          "plan-cascade",
		ObjectiveID: "obj-cascade",
		Status:      domain.PlanStatusExecuting,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := env.plans.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	streamA := &domain.Stream{
		ID:        "stream-a",
		PlanID:    "plan-cascade",
		Title:     "stream A",
		Status:    domain.StreamStatusPending,
		CreatedAt: time.Now(),
	}
	streamB := &domain.Stream{
		ID:           "stream-b",
		PlanID:       "plan-cascade",
		Title:        "stream B",
		Status:       domain.StreamStatusPending,
		Dependencies: []string{"stream-a"},
		CreatedAt:    time.Now(),
	}
	if err := env.streams.Create(ctx, streamA); err != nil {
		t.Fatalf("creating stream A: %v", err)
	}
	if err := env.streams.Create(ctx, streamB); err != nil {
		t.Fatalf("creating stream B: %v", err)
	}

	err := env.coord.Execute(ctx, "obj-cascade")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Wait for completion.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			obj, _ := env.objectives.Get(ctx, "obj-cascade")
			streams, _ := env.streams.ListByPlan(ctx, "plan-cascade")
			t.Fatalf("timed out. objective=%s, streams: %v", obj.Status, streamStatuses(streams))
		default:
		}
		obj, _ := env.objectives.Get(ctx, "obj-cascade")
		if obj.Status == domain.ObjectiveStatusCompleted || obj.Status == domain.ObjectiveStatusPartial || obj.Status == domain.ObjectiveStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Both streams should have been processed.
	streams, _ := env.streams.ListByPlan(ctx, "plan-cascade")
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}
}

func TestHandleBlueprintRefStep_FailedStreamEscalates(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.coord.ctx = ctx

	// Override agent handler to fail.
	env.engine.RegisterHandler(blueprint.StepTypeAgent, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  "intentional test failure",
		}, nil
	})

	env.createObjective(t, "obj-fail", domain.ObjectiveStatusApproved)
	env.createPlan(t, "plan-fail", "obj-fail", []string{"fail-stream"})

	err := env.coord.Execute(ctx, "obj-fail")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Wait for completion.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for execution")
		default:
		}
		obj, _ := env.objectives.Get(ctx, "obj-fail")
		if obj.Status == domain.ObjectiveStatusCompleted || obj.Status == domain.ObjectiveStatusPartial || obj.Status == domain.ObjectiveStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Objective should be partial (stream failed but execution completed).
	obj, _ := env.objectives.Get(ctx, "obj-fail")
	if obj.Status != domain.ObjectiveStatusPartial && obj.Status != domain.ObjectiveStatusFailed {
		t.Fatalf("expected partial or failed, got %s", obj.Status)
	}
}

type sequenceRuntime struct {
	results  []runtime.AgentResult
	spawned  int
	lastOpts []runtime.AgentOpts
}

func (r *sequenceRuntime) Spawn(_ context.Context, _ sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	r.lastOpts = append(r.lastOpts, opts)
	idx := r.spawned
	r.spawned++
	return &handlersTestProcess{result: r.results[idx]}, nil
}

func (r *sequenceRuntime) Name() string        { return "sequence" }
func (r *sequenceRuntime) SupportsRPC() bool   { return false }
func (r *sequenceRuntime) SupportsHooks() bool { return false }

func TestHandleAgentStep_RetriesTransientFailureAndRecordsAttempt(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.createObjective(t, "obj-retry", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-retry", "obj-retry", []string{"stream-1"})

	rt := &sequenceRuntime{results: []runtime.AgentResult{{Success: false, Error: "provider rate limit"}, {Success: true, Summary: "done"}}}
	sp := newMockSandboxProvider()
	recorder, err := observability.New(t.TempDir(), env.eventBus, slog.Default())
	if err != nil {
		t.Fatalf("New recorder: %v", err)
	}
	spawner := NewSpawner(env.agents, rt, sp, rules.NewEngine(slog.Default()), tools.NewCurator(slog.Default()), env.eventBus, recorder, nil, config.RuntimeAuthConfig{}, slog.Default(), "http://localhost:8080", "Tack", "tack@local")
	env.coord.spawner = spawner
	env.coord.attempts = env.attempts
	env.coord.tracker = newAgentTracker(spawner, recorder, env.eventBus, config.TimeoutConfig{}, slog.Default())

	exec := &blueprint.Execution{ID: "exec-retry", ObjectiveID: "obj-retry", StreamID: streams[0].ID, StepStates: map[string]*blueprint.StepState{"build": {StepID: "build", Metadata: map[string]string{}}}}
	step := &blueprint.Step{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder", Commit: "none"}

	result, err := env.coord.HandleAgentStep(ctx, exec, step)
	if err != nil {
		t.Fatalf("HandleAgentStep: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if rt.spawned != 2 {
		t.Fatalf("spawn count = %d, want 2", rt.spawned)
	}

	attempts, err := env.attempts.ListByExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("ListByExecution: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].Action != domain.RecoveryActionRetrySameStep {
		t.Fatalf("action = %s, want retry_same_step", attempts[0].Action)
	}
	if !strings.Contains(rt.lastOpts[1].Overlay, "## Retry Context") {
		t.Fatalf("second overlay missing retry context\n%s", rt.lastOpts[1].Overlay)
	}
	if !strings.Contains(rt.lastOpts[1].Overlay, "provider_rate_limit") {
		t.Fatalf("second overlay missing failure kind\n%s", rt.lastOpts[1].Overlay)
	}
}

func TestHandleAgentStep_UsesBlueprintRetryProfile(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.createObjective(t, "obj-strict", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-strict", "obj-strict", []string{"stream-1"})

	blueprintDir := t.TempDir()
	if err := os.WriteFile(blueprintDir+"/strict-build.yaml", []byte(`id: strict-build
name: Strict build
retry:
  profile: strict
steps:
  - id: build
    type: agent
    role: builder
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg := blueprint.NewRegistry()
	if err := reg.LoadFromDir(blueprintDir); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	env.coord.engine = blueprint.NewEngine(reg, slog.Default())

	rt := &sequenceRuntime{results: []runtime.AgentResult{{Success: false, Error: "provider rate limit"}, {Success: false, Error: "provider rate limit"}, {Success: true, Summary: "done"}}}
	sp := newMockSandboxProvider()
	recorder, err := observability.New(t.TempDir(), env.eventBus, slog.Default())
	if err != nil {
		t.Fatalf("New recorder: %v", err)
	}
	spawner := NewSpawner(env.agents, rt, sp, rules.NewEngine(slog.Default()), tools.NewCurator(slog.Default()), env.eventBus, recorder, nil, config.RuntimeAuthConfig{}, slog.Default(), "http://localhost:8080", "Tack", "tack@local")
	env.coord.spawner = spawner
	env.coord.attempts = env.attempts
	env.coord.tracker = newAgentTracker(spawner, recorder, env.eventBus, config.TimeoutConfig{}, slog.Default())

	exec := &blueprint.Execution{ID: "exec-strict", BlueprintID: "strict-build", ObjectiveID: "obj-strict", StreamID: streams[0].ID, StepStates: map[string]*blueprint.StepState{"build": {StepID: "build", Metadata: map[string]string{}}}}
	step := &blueprint.Step{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder", Commit: "none"}

	result, err := env.coord.HandleAgentStep(ctx, exec, step)
	if err != nil {
		t.Fatalf("HandleAgentStep: %v", err)
	}
	if result.Status != blueprint.StepStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if rt.spawned != 2 {
		t.Fatalf("spawn count = %d, want 2", rt.spawned)
	}

	attempts, err := env.attempts.ListByExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("ListByExecution: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
	var exhausted bool
	for _, attempt := range attempts {
		if attempt.Status == domain.AttemptStatusExhausted {
			exhausted = true
			break
		}
	}
	if !exhausted {
		t.Fatalf("attempts = %+v, want one exhausted attempt", attempts)
	}
}

func TestHandleAgentStep_DoesNotRetryGenericExitFailure(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.createObjective(t, "obj-output", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-output", "obj-output", []string{"stream-1"})

	rt := &sequenceRuntime{results: []runtime.AgentResult{{Success: false, Error: "exit code 1: test failure"}}}
	sp := newMockSandboxProvider()
	recorder, err := observability.New(t.TempDir(), env.eventBus, slog.Default())
	if err != nil {
		t.Fatalf("New recorder: %v", err)
	}
	spawner := NewSpawner(env.agents, rt, sp, rules.NewEngine(slog.Default()), tools.NewCurator(slog.Default()), env.eventBus, recorder, nil, config.RuntimeAuthConfig{}, slog.Default(), "http://localhost:8080", "Tack", "tack@local")
	env.coord.spawner = spawner
	env.coord.attempts = env.attempts
	env.coord.tracker = newAgentTracker(spawner, recorder, env.eventBus, config.TimeoutConfig{}, slog.Default())

	exec := &blueprint.Execution{ID: "exec-output", BlueprintID: "build-review", ObjectiveID: "obj-output", StreamID: streams[0].ID, StepStates: map[string]*blueprint.StepState{"build": {StepID: "build", Metadata: map[string]string{}}}}
	step := &blueprint.Step{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder", Commit: "none"}

	result, err := env.coord.HandleAgentStep(ctx, exec, step)
	if err != nil {
		t.Fatalf("HandleAgentStep: %v", err)
	}
	if result.Status != blueprint.StepStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if rt.spawned != 1 {
		t.Fatalf("spawn count = %d, want 1", rt.spawned)
	}

	attempts, err := env.attempts.ListByExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("ListByExecution: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].FailureKind != domain.FailureAgentOutput {
		t.Fatalf("failure kind = %s, want %s", attempts[0].FailureKind, domain.FailureAgentOutput)
	}
	if attempts[0].Action == domain.RecoveryActionRetrySameStep {
		t.Fatalf("action = %s, expected non-retry action", attempts[0].Action)
	}
}

func TestBuildReview_RejectionLoopsBackToBuilder(t *testing.T) {
	env := setupDispatchEnv(t)
	ctx := context.Background()
	env.createObjective(t, "obj-review-loop", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-review-loop", "obj-review-loop", []string{"stream-1"})

	rt := &sequenceRuntime{results: []runtime.AgentResult{
		{Success: true, Summary: "builder pass 1"},
		{Success: true, Summary: "REVIEW_DECISION: reject\nREVIEW_FEEDBACK:\nAdd a regression test for the failure path"},
		{Success: true, Summary: "builder pass 2"},
		{Success: true, Summary: "REVIEW_DECISION: approve"},
	}}
	sp := newMockSandboxProvider()
	recorder, err := observability.New(t.TempDir(), env.eventBus, slog.Default())
	if err != nil {
		t.Fatalf("New recorder: %v", err)
	}
	spawner := NewSpawner(env.agents, rt, sp, rules.NewEngine(slog.Default()), tools.NewCurator(slog.Default()), env.eventBus, recorder, nil, config.RuntimeAuthConfig{}, slog.Default(), "http://localhost:8080", "Tack", "tack@local")
	env.coord.spawner = spawner
	env.coord.attempts = env.attempts
	env.coord.tracker = newAgentTracker(spawner, recorder, env.eventBus, config.TimeoutConfig{}, slog.Default())
	handlers := NewHandlers(env.scheduler, nil, env.lifecycle, &handlersTestMergeHelper{}, env.engine, rt, env.plans, env.streams, env.objectives, env.executions, env.agents, env.attempts, sp, env.eventBus, recorder, "main", nil, config.RuntimeAuthConfig{}, "", slog.Default())
	env.engine.RegisterHandler(blueprint.StepTypeAgent, env.coord.HandleAgentStep)
	env.engine.RegisterHandler(blueprint.StepTypeDeterministic, handlers.HandleDeterministic)

	exec, err := env.engine.Start(ctx, "build-review", "obj-review-loop")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	exec.StreamID = streams[0].ID
	for exec.Status == "running" {
		exec, err = env.engine.Advance(ctx, exec)
		if err != nil {
			t.Fatalf("Advance: %v", err)
		}
	}

	if exec.Status != "completed" {
		t.Fatalf("status = %s, want completed", exec.Status)
	}
	if rt.spawned != 4 {
		t.Fatalf("spawn count = %d, want 4", rt.spawned)
	}
	if !strings.Contains(rt.lastOpts[2].Overlay, "review_rejection") {
		t.Fatalf("builder rerun missing review rejection context\n%s", rt.lastOpts[2].Overlay)
	}
	if !strings.Contains(rt.lastOpts[2].Overlay, "Add a regression test for the failure path") {
		t.Fatalf("builder rerun missing reviewer feedback\n%s", rt.lastOpts[2].Overlay)
	}
	attempts, err := env.attempts.ListByExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("ListByExecution: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].FailureKind != domain.FailureReviewRejection {
		t.Fatalf("failure kind = %s, want %s", attempts[0].FailureKind, domain.FailureReviewRejection)
	}
	if attempts[0].Action != domain.RecoveryActionRerunPreviousAgent {
		t.Fatalf("action = %s, want %s", attempts[0].Action, domain.RecoveryActionRerunPreviousAgent)
	}
}

func streamStatuses(streams []domain.Stream) string {
	var s string
	for _, st := range streams {
		s += fmt.Sprintf("%s=%s ", st.ID, st.Status)
	}
	return s
}
