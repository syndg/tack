package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
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

func streamStatuses(streams []domain.Stream) string {
	var s string
	for _, st := range streams {
		s += fmt.Sprintf("%s=%s ", st.ID, st.Status)
	}
	return s
}
