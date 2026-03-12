package blueprint

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
)

func newTestRegistry(t *testing.T, bp *Blueprint) *Registry {
	t.Helper()
	r := NewRegistry()
	r.blueprints[bp.Name] = bp
	return r
}

func testBlueprint() *Blueprint {
	return &Blueprint{
		Name: "test-bp",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "planner", Next: "s2"},
			{ID: "s2", Type: StepTypeDeterministic, Action: "lint", Next: "s3"},
			{ID: "s3", Type: StepTypeHuman},
		},
	}
}

func TestStart_InitializesStepStates(t *testing.T) {
	bp := testBlueprint()
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())

	exec, err := engine.Start(context.Background(), "test-bp", "obj-1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if exec.Status != "running" {
		t.Errorf("status = %q, want %q", exec.Status, "running")
	}
	if exec.CurrentStep != "s1" {
		t.Errorf("current_step = %q, want %q", exec.CurrentStep, "s1")
	}
	if exec.BlueprintName != "test-bp" {
		t.Errorf("blueprint_name = %q, want %q", exec.BlueprintName, "test-bp")
	}
	if exec.ObjectiveID != "obj-1" {
		t.Errorf("objective_id = %q, want %q", exec.ObjectiveID, "obj-1")
	}
	if len(exec.StepStates) != 3 {
		t.Fatalf("step_states count = %d, want 3", len(exec.StepStates))
	}
	for _, step := range bp.Steps {
		state, ok := exec.StepStates[step.ID]
		if !ok {
			t.Errorf("missing step state for %q", step.ID)
			continue
		}
		if state.Status != StepStatusPending {
			t.Errorf("step %q status = %q, want %q", step.ID, state.Status, StepStatusPending)
		}
	}
}

func TestAdvance_CallsHandlerAndMoves(t *testing.T) {
	bp := testBlueprint()
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())

	var calledSteps []string
	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		calledSteps = append(calledSteps, step.ID)
		return StepResult{Status: StepStatusCompleted}, nil
	})
	engine.RegisterHandler(StepTypeDeterministic, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		calledSteps = append(calledSteps, step.ID)
		return StepResult{Status: StepStatusCompleted}, nil
	})

	exec, _ := engine.Start(context.Background(), "test-bp", "obj-1")

	// Advance step s1 (agent).
	exec, err := engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance s1: %v", err)
	}
	if exec.CurrentStep != "s2" {
		t.Errorf("after s1: current_step = %q, want %q", exec.CurrentStep, "s2")
	}

	// Advance step s2 (deterministic).
	exec, err = engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance s2: %v", err)
	}
	if exec.CurrentStep != "s3" {
		t.Errorf("after s2: current_step = %q, want %q", exec.CurrentStep, "s3")
	}

	if len(calledSteps) != 2 {
		t.Fatalf("handler called %d times, want 2", len(calledSteps))
	}
	if calledSteps[0] != "s1" || calledSteps[1] != "s2" {
		t.Errorf("handler called for %v, want [s1, s2]", calledSteps)
	}
}

func TestAdvance_RetryOnFailure(t *testing.T) {
	bp := &Blueprint{
		Name: "retry-bp",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "dev", Retry: 2},
		},
	}
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())

	attempt := 0
	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		attempt++
		if attempt <= 2 {
			return StepResult{Status: StepStatusFailed, Error: fmt.Sprintf("fail-%d", attempt)}, nil
		}
		return StepResult{Status: StepStatusCompleted}, nil
	})

	exec, _ := engine.Start(context.Background(), "retry-bp", "obj-1")

	// First advance: fails, retry 1.
	exec, err := engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance 1: %v", err)
	}
	if exec.StepStates["s1"].RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", exec.StepStates["s1"].RetryCount)
	}
	if exec.Status != "running" {
		t.Errorf("status = %q, want %q", exec.Status, "running")
	}

	// Second advance: fails, retry 2.
	exec, _ = engine.Advance(context.Background(), exec)
	if exec.StepStates["s1"].RetryCount != 2 {
		t.Errorf("retry_count = %d, want 2", exec.StepStates["s1"].RetryCount)
	}

	// Third advance: succeeds (retry count exhausted, but handler returns success now).
	exec, _ = engine.Advance(context.Background(), exec)
	if exec.Status != "completed" {
		t.Errorf("status = %q, want %q", exec.Status, "completed")
	}
}

func TestApproveHuman_UnblocksExecution(t *testing.T) {
	bp := &Blueprint{
		Name: "human-bp",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "dev", Next: "s2"},
			{ID: "s2", Type: StepTypeHuman, Next: "s3"},
			{ID: "s3", Type: StepTypeDeterministic, Action: "done"},
		},
	}
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())
	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		return StepResult{Status: StepStatusCompleted}, nil
	})

	exec, _ := engine.Start(context.Background(), "human-bp", "obj-1")

	// Advance past agent step.
	exec, _ = engine.Advance(context.Background(), exec)
	if exec.CurrentStep != "s2" {
		t.Fatalf("current_step = %q, want s2", exec.CurrentStep)
	}

	// Advance on human step — should block.
	exec, _ = engine.Advance(context.Background(), exec)
	if exec.Status != "waiting_human" {
		t.Fatalf("status = %q, want waiting_human", exec.Status)
	}

	// Approve the human step.
	exec, err := engine.ApproveHuman(context.Background(), exec)
	if err != nil {
		t.Fatalf("ApproveHuman: %v", err)
	}
	if exec.Status != "running" {
		t.Errorf("status after approve = %q, want running", exec.Status)
	}
	if exec.CurrentStep != "s3" {
		t.Errorf("current_step = %q, want s3", exec.CurrentStep)
	}
	if exec.StepStates["s2"].Status != StepStatusCompleted {
		t.Errorf("s2 status = %q, want completed", exec.StepStates["s2"].Status)
	}
}

func TestAdvance_PersistsOutputAndMetadata(t *testing.T) {
	bp := &Blueprint{
		Name:  "metadata-bp",
		Steps: []Step{{ID: "s1", Type: StepTypeAgent, Role: "builder"}},
	}
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())
	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		return StepResult{
			Status: StepStatusCompleted,
			Output: "done",
			Metadata: map[string]string{
				"commit_message": "feat: add generated metadata",
			},
		}, nil
	})

	exec, _ := engine.Start(context.Background(), "metadata-bp", "obj-1")
	exec, err := engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}

	state := exec.StepStates["s1"]
	if state.Output != "done" {
		t.Fatalf("output = %q, want done", state.Output)
	}
	if state.Metadata["commit_message"] != "feat: add generated metadata" {
		t.Fatalf("metadata = %v, missing commit_message", state.Metadata)
	}
}

func TestAdvance_OnFailRoutesBackToAgentWithFixContext(t *testing.T) {
	bp := &Blueprint{
		Name: "fix-loop-bp",
		Steps: []Step{
			{ID: "fix", Type: StepTypeAgent, Role: "builder", Next: "lint"},
			{ID: "lint", Type: StepTypeDeterministic, Action: "run_quality_gates", Retry: 1, OnFail: "fix", MaxFixIterations: 2},
		},
	}
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())

	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		return StepResult{Status: StepStatusCompleted}, nil
	})

	lintAttempts := 0
	engine.RegisterHandler(StepTypeDeterministic, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		lintAttempts++
		return StepResult{Status: StepStatusFailed, Error: fmt.Sprintf("gate failure %d", lintAttempts)}, nil
	})

	exec, _ := engine.Start(context.Background(), "fix-loop-bp", "obj-1")

	// Run fix once.
	exec, err := engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance fix: %v", err)
	}
	if exec.CurrentStep != "lint" {
		t.Fatalf("current_step = %q, want lint", exec.CurrentStep)
	}

	// First lint failure should use normal retry behavior.
	exec, err = engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance lint retry: %v", err)
	}
	lintState := exec.StepStates["lint"]
	if lintState.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", lintState.RetryCount)
	}
	if exec.CurrentStep != "lint" {
		t.Fatalf("current_step after retry = %q, want lint", exec.CurrentStep)
	}

	// Second lint failure should route back to fix.
	exec, err = engine.Advance(context.Background(), exec)
	if err != nil {
		t.Fatalf("Advance lint on_fail: %v", err)
	}
	lintState = exec.StepStates["lint"]
	if exec.CurrentStep != "fix" {
		t.Fatalf("current_step after on_fail = %q, want fix", exec.CurrentStep)
	}
	if lintState.FixIterations != 1 {
		t.Fatalf("fix_iterations = %d, want 1", lintState.FixIterations)
	}
	if lintState.RetryCount != 0 {
		t.Fatalf("retry_count after on_fail = %d, want 0", lintState.RetryCount)
	}
	fixState := exec.StepStates["fix"]
	if got := fixState.Metadata["fix_context"]; got != "gate failure 2" {
		t.Fatalf("fix_context = %q, want %q", got, "gate failure 2")
	}
}

func TestAdvance_OnFailStopsAfterMaxFixIterations(t *testing.T) {
	bp := &Blueprint{
		Name: "fix-loop-limit-bp",
		Steps: []Step{
			{ID: "fix", Type: StepTypeAgent, Role: "builder", Next: "lint"},
			{ID: "lint", Type: StepTypeDeterministic, Action: "run_quality_gates", OnFail: "fix", MaxFixIterations: 2},
		},
	}
	reg := newTestRegistry(t, bp)
	engine := NewEngine(reg, slog.Default())

	engine.RegisterHandler(StepTypeAgent, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		return StepResult{Status: StepStatusCompleted}, nil
	})
	engine.RegisterHandler(StepTypeDeterministic, func(ctx context.Context, exec *Execution, step *Step) (StepResult, error) {
		return StepResult{Status: StepStatusFailed, Error: "still failing"}, nil
	})

	exec, _ := engine.Start(context.Background(), "fix-loop-limit-bp", "obj-1")

	// 1st fix
	exec, _ = engine.Advance(context.Background(), exec)
	// 1st lint fail -> reroute (iteration 1)
	exec, _ = engine.Advance(context.Background(), exec)
	// 2nd fix
	exec, _ = engine.Advance(context.Background(), exec)
	// 2nd lint fail -> reroute (iteration 2)
	exec, _ = engine.Advance(context.Background(), exec)
	// 3rd fix
	exec, _ = engine.Advance(context.Background(), exec)
	// 3rd lint fail -> execution should fail, limit exhausted
	exec, _ = engine.Advance(context.Background(), exec)

	if exec.Status != "failed" {
		t.Fatalf("status = %q, want failed", exec.Status)
	}
	if exec.StepStates["lint"].FixIterations != 2 {
		t.Fatalf("fix_iterations = %d, want 2", exec.StepStates["lint"].FixIterations)
	}
}
