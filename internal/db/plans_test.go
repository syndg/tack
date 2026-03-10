package db

import (
	"context"
	"testing"

	"github.com/syndg/deck/internal/domain"
)

func TestPlanStore_Create_AutoUUID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test objective")

	store := NewPlanStore(d.Conn())
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{"go test ./..."}}
	if err := store.Create(ctx, plan); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if plan.ID == "" {
		t.Error("expected UUID to be generated")
	}
	if plan.Status != domain.PlanStatusDraft {
		t.Errorf("status = %q, want %q", plan.Status, domain.PlanStatusDraft)
	}
}

func TestPlanStore_Get(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")

	store := NewPlanStore(d.Conn())
	plan := &domain.Plan{
		ObjectiveID:  obj.ID,
		QualityGates: []string{"go test ./...", "go vet ./..."},
	}
	if err := store.Create(ctx, plan); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != plan.ID {
		t.Errorf("ID = %q, want %q", got.ID, plan.ID)
	}
	if got.ObjectiveID != obj.ID {
		t.Errorf("ObjectiveID = %q, want %q", got.ObjectiveID, obj.ID)
	}
	if len(got.QualityGates) != 2 {
		t.Errorf("QualityGates len = %d, want 2", len(got.QualityGates))
	}
	if got.QualityGates[0] != "go test ./..." {
		t.Errorf("QualityGates[0] = %q, want \"go test ./...\"", got.QualityGates[0])
	}
}

func TestPlanStore_Get_NotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewPlanStore(d.Conn())

	_, err := store.Get(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

func TestPlanStore_GetByObjective(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")

	store := NewPlanStore(d.Conn())
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := store.Create(ctx, plan); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if got.ID != plan.ID {
		t.Errorf("ID = %q, want %q", got.ID, plan.ID)
	}
	if got.ObjectiveID != obj.ID {
		t.Errorf("ObjectiveID = %q, want %q", got.ObjectiveID, obj.ID)
	}
}

func TestPlanStore_GetByObjective_NotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewPlanStore(d.Conn())

	_, err := store.GetByObjective(ctx, "missing-obj-id")
	if err == nil {
		t.Fatal("expected error for objective with no plans")
	}
}

func TestPlanStore_UpdateStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")

	store := NewPlanStore(d.Conn())
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	store.Create(ctx, plan)

	if err := store.UpdateStatus(ctx, plan.ID, domain.PlanStatusApproved); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := store.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get after UpdateStatus: %v", err)
	}
	if got.Status != domain.PlanStatusApproved {
		t.Errorf("status = %q, want %q", got.Status, domain.PlanStatusApproved)
	}
}

func TestPlanStore_List_ReturnsAllPlans(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")

	store := NewPlanStore(d.Conn())
	var created []*domain.Plan
	for i := 0; i < 3; i++ {
		p := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create plan %d: %v", i, err)
		}
		created = append(created, p)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len = %d, want 3", len(list))
	}
	// All returned plans should have the correct objective_id.
	for _, p := range list {
		if p.ObjectiveID != obj.ID {
			t.Errorf("plan %q has ObjectiveID %q, want %q", p.ID, p.ObjectiveID, obj.ID)
		}
	}
}
