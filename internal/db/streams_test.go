package db

import (
	"context"
	"testing"

	"github.com/syndg/deck/internal/domain"
)

// createTestPlan inserts a minimal plan for the given objective and returns it.
func createTestPlan(t *testing.T, store *PlanStore, objectiveID string) *domain.Plan {
	t.Helper()
	plan := &domain.Plan{ObjectiveID: objectiveID, QualityGates: []string{}}
	if err := store.Create(context.Background(), plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	return plan
}

func TestStreamStore_Create_JSONSerialization(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)

	store := NewStreamStore(d.Conn())
	stream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "auth stream",
		Description:  "Handle authentication",
		FileScope:    []string{"src/auth/**", "src/jwt/**"},
		Dependencies: []string{},
	}
	if err := store.Create(ctx, stream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stream.ID == "" {
		t.Error("expected UUID to be generated")
	}
	if stream.Status != "pending" {
		t.Errorf("status = %q, want pending", stream.Status)
	}
}

func TestStreamStore_Get_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)

	store := NewStreamStore(d.Conn())
	// Dependencies stores UUIDs — use a placeholder (no FK constraint on stream dependencies).
	stream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "my stream",
		Description:  "does things",
		FileScope:    []string{"src/**/*.go"},
		Dependencies: []string{"dep-placeholder-id"},
	}
	if err := store.Create(ctx, stream); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "my stream" {
		t.Errorf("Title = %q, want \"my stream\"", got.Title)
	}
	if got.Description != "does things" {
		t.Errorf("Description = %q, want \"does things\"", got.Description)
	}
	if len(got.FileScope) != 1 || got.FileScope[0] != "src/**/*.go" {
		t.Errorf("FileScope = %v, want [\"src/**/*.go\"]", got.FileScope)
	}
	if len(got.Dependencies) != 1 || got.Dependencies[0] != "dep-placeholder-id" {
		t.Errorf("Dependencies = %v, want [\"dep-placeholder-id\"]", got.Dependencies)
	}
	if got.PlanID != plan.ID {
		t.Errorf("PlanID = %q, want %q", got.PlanID, plan.ID)
	}
}

func TestStreamStore_ListByPlan(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)

	store := NewStreamStore(d.Conn())
	for i := 0; i < 3; i++ {
		s := &domain.Stream{
			PlanID:       plan.ID,
			Title:        "stream",
			FileScope:    []string{"**/*"},
			Dependencies: []string{},
		}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create stream %d: %v", i, err)
		}
	}

	streams, err := store.ListByPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListByPlan: %v", err)
	}
	if len(streams) != 3 {
		t.Errorf("len = %d, want 3", len(streams))
	}
	for _, s := range streams {
		if s.PlanID != plan.ID {
			t.Errorf("stream %q has PlanID %q, want %q", s.ID, s.PlanID, plan.ID)
		}
	}
}

func TestStreamStore_UpdateStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)

	store := NewStreamStore(d.Conn())
	stream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "s",
		FileScope:    []string{"**"},
		Dependencies: []string{},
	}
	store.Create(ctx, stream)

	if err := store.UpdateStatus(ctx, stream.ID, "completed"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err := store.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

// TestStreamStore_ListReady_DependencyResolution tests the three-stream scenario:
// stream 3 depends on streams 1 and 2.
// Only after both 1 and 2 are completed should stream 3 become ready.
func TestStreamStore_ListReady_DependencyResolution(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)

	store := NewStreamStore(d.Conn())

	// s1 and s2 have no dependencies; s3 depends on both.
	s1 := &domain.Stream{PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{}}
	s2 := &domain.Stream{PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{}}
	if err := store.Create(ctx, s1); err != nil {
		t.Fatalf("Create s1: %v", err)
	}
	if err := store.Create(ctx, s2); err != nil {
		t.Fatalf("Create s2: %v", err)
	}

	s3 := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "s3",
		FileScope:    []string{"c/**"},
		Dependencies: []string{s1.ID, s2.ID},
	}
	if err := store.Create(ctx, s3); err != nil {
		t.Fatalf("Create s3: %v", err)
	}

	// Initially: s1 and s2 are ready (no deps); s3 is not.
	ready, err := store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (initial): %v", err)
	}
	readyIDs := idsSet(ready)
	if !readyIDs[s1.ID] || !readyIDs[s2.ID] {
		t.Error("s1 and s2 should initially be ready")
	}
	if readyIDs[s3.ID] {
		t.Error("s3 should not be ready initially")
	}

	// Mark s1 completed — s3 still not ready (s2 still pending).
	if err := store.UpdateStatus(ctx, s1.ID, "completed"); err != nil {
		t.Fatalf("UpdateStatus s1: %v", err)
	}
	ready, err = store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (after s1 done): %v", err)
	}
	readyIDs = idsSet(ready)
	if readyIDs[s3.ID] {
		t.Error("s3 should not be ready when only s1 is completed")
	}

	// Mark s2 completed — s3 is now ready.
	if err := store.UpdateStatus(ctx, s2.ID, "completed"); err != nil {
		t.Fatalf("UpdateStatus s2: %v", err)
	}
	ready, err = store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (after s2 done): %v", err)
	}
	readyIDs = idsSet(ready)
	if !readyIDs[s3.ID] {
		t.Error("s3 should be ready when both s1 and s2 are completed")
	}
}

// idsSet converts a stream slice to a set of IDs for easy lookup.
func idsSet(streams []domain.Stream) map[string]bool {
	m := make(map[string]bool, len(streams))
	for _, s := range streams {
		m[s.ID] = true
	}
	return m
}
