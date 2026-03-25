package dispatch

import (
	"context"
	"testing"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
)

func TestHandleDeterministic_UnknownAction(t *testing.T) {
	env := setupDispatchEnv(t)

	h := &Handlers{
		scheduler:  env.scheduler,
		plans:      env.plans,
		streams:    env.streams,
		objectives: env.objectives,
		executions: env.executions,
		agents:     env.agents,
		eventBus:   env.eventBus,
		lifecycle:  env.lifecycle,
		baseBranch: "main",
		logger:     env.logger,
	}

	exec := &blueprint.Execution{ID: "exec-1", ObjectiveID: "obj-1"}
	step := &blueprint.Step{ID: "step-1", Type: blueprint.StepTypeDeterministic, Action: "nonexistent_action"}

	result, err := h.HandleDeterministic(context.Background(), exec, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if result.Error == "" {
		t.Fatal("expected error message for unknown action")
	}
}

func TestHandleDeterministic_DispatchStreams(t *testing.T) {
	env := setupDispatchEnv(t)

	env.createObjective(t, "obj-dispatch", domain.ObjectiveStatusExecuting)
	env.createPlan(t, "plan-dispatch", "obj-dispatch", []string{"s1", "s2"})

	h := &Handlers{
		scheduler:  env.scheduler,
		plans:      env.plans,
		streams:    env.streams,
		objectives: env.objectives,
		executions: env.executions,
		agents:     env.agents,
		eventBus:   env.eventBus,
		lifecycle:  env.lifecycle,
		baseBranch: "main",
		logger:     env.logger,
	}

	exec := &blueprint.Execution{ID: "exec-2", ObjectiveID: "obj-dispatch"}
	step := &blueprint.Step{ID: "step-dispatch", Type: blueprint.StepTypeDeterministic, Action: "dispatch_streams"}

	result, err := h.HandleDeterministic(context.Background(), exec, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("expected completed, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestGeneratedMessagesFromSource(t *testing.T) {
	exec := &blueprint.Execution{
		StepStates: map[string]*blueprint.StepState{
			"fix": {
				StepID: "fix",
				Metadata: map[string]string{
					"pr_title": "Fix flaky dispatch flow",
					"pr_body":  "## Summary\n- stabilize dispatch flow",
				},
			},
		},
	}

	msgs := generatedMessagesFromSource(exec, "fix")
	if msgs.PRTitle != "Fix flaky dispatch flow" {
		t.Fatalf("PRTitle = %q, want generated title", msgs.PRTitle)
	}
	if msgs.PRBody == "" {
		t.Fatal("expected PRBody to be populated")
	}
}
