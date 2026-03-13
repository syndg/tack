package lifecycle

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

// setupManager opens a fresh DB and constructs a lifecycle Manager for testing.
func setupManager(t *testing.T) (*Manager, *db.ObjectiveStore, *db.PlanStore) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	objStore := db.NewObjectiveStore(d.Conn())
	planStore := db.NewPlanStore(d.Conn())
	streamStore := db.NewStreamStore(d.Conn())
	agentStore := db.NewAgentStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())

	bus := events.NewPersistentBus(eventStore, slog.Default())
	mgr := New(objStore, planStore, streamStore, agentStore, bus, slog.Default())
	return mgr, objStore, planStore
}

func TestIsValidTransition_ValidPaths(t *testing.T) {
	valid := []struct{ from, to domain.ObjectiveStatus }{
		{domain.ObjectiveStatusPlanning, domain.ObjectiveStatusApproved},
		{domain.ObjectiveStatusPlanning, domain.ObjectiveStatusFailed},
		{domain.ObjectiveStatusApproved, domain.ObjectiveStatusExecuting},
		{domain.ObjectiveStatusApproved, domain.ObjectiveStatusFailed},
		{domain.ObjectiveStatusExecuting, domain.ObjectiveStatusCompleted},
		{domain.ObjectiveStatusExecuting, domain.ObjectiveStatusPartial},
		{domain.ObjectiveStatusExecuting, domain.ObjectiveStatusFailed},
		{domain.ObjectiveStatusPartial, domain.ObjectiveStatusCompleted},
		{domain.ObjectiveStatusFailed, domain.ObjectiveStatusPlanning},
	}
	for _, tt := range valid {
		if !IsValidTransition(tt.from, tt.to) {
			t.Errorf("expected valid: %s → %s", tt.from, tt.to)
		}
	}
}

func TestIsValidTransition_InvalidPaths(t *testing.T) {
	invalid := []struct{ from, to domain.ObjectiveStatus }{
		{domain.ObjectiveStatusPlanning, domain.ObjectiveStatusCompleted},
		{domain.ObjectiveStatusPlanning, domain.ObjectiveStatusExecuting},
		{domain.ObjectiveStatusApproved, domain.ObjectiveStatusCompleted},
		{domain.ObjectiveStatusCompleted, domain.ObjectiveStatusPlanning},
		{domain.ObjectiveStatusCompleted, domain.ObjectiveStatusFailed},
	}
	for _, tt := range invalid {
		if IsValidTransition(tt.from, tt.to) {
			t.Errorf("expected invalid: %s → %s", tt.from, tt.to)
		}
	}
}

func TestTransition_ValidSucceeds(t *testing.T) {
	mgr, objStore, _ := setupManager(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusPlanning}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	if err := mgr.Transition(ctx, obj.ID, domain.ObjectiveStatusApproved); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	got, err := objStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if got.Status != domain.ObjectiveStatusApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}
}

func TestTransition_InvalidFails(t *testing.T) {
	mgr, objStore, _ := setupManager(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusPlanning}
	objStore.Create(ctx, obj)

	// planning → completed is not a valid transition.
	if err := mgr.Transition(ctx, obj.ID, domain.ObjectiveStatusCompleted); err == nil {
		t.Fatal("expected error for invalid transition planning → completed")
	}
}

func TestApprovePlan_UpdatesBothStatuses(t *testing.T) {
	mgr, objStore, planStore := setupManager(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusPlanning}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	if err := mgr.ApprovePlan(ctx, plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	gotPlan, err := planStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get plan: %v", err)
	}
	if gotPlan.Status != domain.PlanStatusApproved {
		t.Errorf("plan status = %q, want approved", gotPlan.Status)
	}

	gotObj, err := objStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if gotObj.Status != domain.ObjectiveStatusApproved {
		t.Errorf("objective status = %q, want approved", gotObj.Status)
	}
}

func TestRejectPlan_KeepsObjectiveInPlanning(t *testing.T) {
	mgr, objStore, planStore := setupManager(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusPlanning}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	if err := mgr.RejectPlan(ctx, plan.ID); err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}

	gotPlan, err := planStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get plan: %v", err)
	}
	if gotPlan.Status != domain.PlanStatusFailed {
		t.Errorf("plan status = %q, want failed", gotPlan.Status)
	}

	// Objective should remain in planning — RejectPlan does not change objective status.
	gotObj, err := objStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if gotObj.Status != domain.ObjectiveStatusPlanning {
		t.Errorf("objective status = %q, want planning (unchanged)", gotObj.Status)
	}
}
