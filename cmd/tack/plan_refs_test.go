package main

import (
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestResolvePlanFromList_PrefersLatestPendingApproval(t *testing.T) {
	plans := []domain.Plan{
		{ID: "plan-completed", ObjectiveID: "objective-completed", Status: domain.PlanStatusCompleted, CreatedAt: time.Unix(20, 0)},
		{ID: "plan-pending", ObjectiveID: "objective-pending", Status: domain.PlanStatusPendingApproval, CreatedAt: time.Unix(10, 0)},
	}

	plan, err := resolvePlanFromList(plans, "", planResolveOptions{preferPendingApproval: true})
	if err != nil {
		t.Fatalf("resolvePlanFromList: %v", err)
	}
	if plan.ID != "plan-pending" {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, "plan-pending")
	}
}

func TestResolvePlanFromListWithObjectives_RejectsMultiplePendingPlansWithObjectiveLabels(t *testing.T) {
	plans := []domain.Plan{
		{ID: "plan-a-1111", ObjectiveID: "objective-a", Status: domain.PlanStatusPendingApproval, CreatedAt: time.Unix(20, 0)},
		{ID: "plan-b-2222", ObjectiveID: "objective-b", Status: domain.PlanStatusPendingApproval, CreatedAt: time.Unix(10, 0)},
	}
	objectives := map[string]domain.Objective{
		"objective-a": {ID: "objective-a", Description: "Add OAuth refresh recovery"},
		"objective-b": {ID: "objective-b", Description: "Build benchmark comparison UI with filters and summaries"},
	}

	_, err := resolvePlanFromListWithObjectives(plans, "", planResolveOptions{preferPendingApproval: true}, objectives)
	if err == nil {
		t.Fatal("expected multiple pending plans error")
	}
	msg := err.Error()
	for _, want := range []string{"multiple pending plans", "Add OAuth refresh recovery", "Build benchmark comparison UI"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestResolvePlanFromList_FallsBackToLatestPlan(t *testing.T) {
	plans := []domain.Plan{
		{ID: "plan-latest", ObjectiveID: "objective-latest", Status: domain.PlanStatusCompleted, CreatedAt: time.Unix(20, 0)},
		{ID: "plan-older", ObjectiveID: "objective-older", Status: domain.PlanStatusFailed, CreatedAt: time.Unix(10, 0)},
	}

	plan, err := resolvePlanFromList(plans, "", planResolveOptions{preferPendingApproval: true})
	if err != nil {
		t.Fatalf("resolvePlanFromList: %v", err)
	}
	if plan.ID != "plan-latest" {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, "plan-latest")
	}
}

func TestResolvePlanFromList_MatchesPlanPrefix(t *testing.T) {
	plans := []domain.Plan{
		{ID: "12345678-aaaa-bbbb-cccc-000000000001", ObjectiveID: "objective-1", Status: domain.PlanStatusPendingApproval},
		{ID: "87654321-aaaa-bbbb-cccc-000000000002", ObjectiveID: "objective-2", Status: domain.PlanStatusPendingApproval},
	}

	plan, err := resolvePlanFromList(plans, "12345678", planResolveOptions{})
	if err != nil {
		t.Fatalf("resolvePlanFromList: %v", err)
	}
	if plan.ID != plans[0].ID {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, plans[0].ID)
	}
}

func TestResolvePlanFromList_MatchesObjectivePrefixToLatestPlan(t *testing.T) {
	plans := []domain.Plan{
		{ID: "plan-new", ObjectiveID: "objective-12345678-aaaa", Status: domain.PlanStatusPendingApproval, CreatedAt: time.Unix(20, 0)},
		{ID: "plan-old", ObjectiveID: "objective-12345678-aaaa", Status: domain.PlanStatusDraft, CreatedAt: time.Unix(10, 0)},
		{ID: "plan-other", ObjectiveID: "objective-87654321-bbbb", Status: domain.PlanStatusPendingApproval, CreatedAt: time.Unix(5, 0)},
	}

	plan, err := resolvePlanFromList(plans, "objective-12345678", planResolveOptions{})
	if err != nil {
		t.Fatalf("resolvePlanFromList: %v", err)
	}
	if plan.ID != "plan-new" {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, "plan-new")
	}
}

func TestResolvePlanFromList_RecognizesLatestAlias(t *testing.T) {
	plans := []domain.Plan{{ID: "plan-pending", ObjectiveID: "objective-pending", Status: domain.PlanStatusPendingApproval}}

	plan, err := resolvePlanFromList(plans, "latest", planResolveOptions{preferPendingApproval: true})
	if err != nil {
		t.Fatalf("resolvePlanFromList: %v", err)
	}
	if plan.ID != "plan-pending" {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, "plan-pending")
	}
}

func TestResolvePlanFromList_RejectsAmbiguousPrefix(t *testing.T) {
	plans := []domain.Plan{
		{ID: "abcd1111-aaaa-bbbb-cccc-000000000001", ObjectiveID: "objective-1", Status: domain.PlanStatusPendingApproval},
		{ID: "abcd2222-aaaa-bbbb-cccc-000000000002", ObjectiveID: "objective-2", Status: domain.PlanStatusPendingApproval},
	}

	_, err := resolvePlanFromList(plans, "abcd", planResolveOptions{})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %q, want ambiguity message", err)
	}
}

func TestTruncateText_CollapsesWhitespaceAndAddsEllipsis(t *testing.T) {
	got := truncateObjectiveText("Build a large\n\tobjective with plenty of context", 32)
	want := "Build a large objective with..."
	if got != want {
		t.Fatalf("truncateObjectiveText() = %q, want %q", got, want)
	}
}

func TestObjectiveLabel_UsesObjectiveDescription(t *testing.T) {
	plan := domain.Plan{ObjectiveID: "objective-1"}
	objectives := map[string]domain.Objective{
		"objective-1": {ID: "objective-1", Description: "Add a useful human label"},
	}

	got := objectiveLabel(plan, objectives)
	if got != "Add a useful human label" {
		t.Fatalf("objectiveLabel() = %q", got)
	}
}
