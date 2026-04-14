package db

import (
	"context"
	"errors"
	"testing"

	"github.com/syndg/tack/internal/domain"
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
	if stream.Status != domain.StreamStatusPending {
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
		Card:         &domain.StreamCard{Goal: "do the thing", ProofScope: []string{"go test ./..."}, HardAnchors: []domain.StreamCardAnchor{{Instruction: "stay in middleware", Citations: []domain.StreamCardCitation{{ID: "rule:1", Detail: "middleware rule"}}}}, ContractPatches: []domain.StreamCardPatch{{Instruction: "Honor this explicit human retry guidance: keep middleware stable", Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance}}},
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
	if got.Card == nil || got.Card.Goal != "do the thing" {
		t.Fatalf("Card = %#v, want persisted card", got.Card)
	}
	if len(got.Card.HardAnchors) != 1 || got.Card.HardAnchors[0].Citations[0].ID != "rule:1" {
		t.Fatalf("hard anchors = %#v", got.Card.HardAnchors)
	}
	if len(got.Card.ContractPatches) != 1 || got.Card.ContractPatches[0].Instruction == "" {
		t.Fatalf("contract patches = %#v", got.Card.ContractPatches)
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

	// Walk valid path: pending → executing → completed
	if err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("UpdateStatus to executing: %v", err)
	}
	if err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus to completed: %v", err)
	}
	got, err := store.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.StreamStatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

// advanceStream walks a stream through valid transitions to reach the target status.
func advanceStream(t *testing.T, store *StreamStore, ctx context.Context, id string, target domain.StreamStatus) {
	t.Helper()
	paths := map[domain.StreamStatus][]domain.StreamStatus{
		domain.StreamStatusExecuting:  {domain.StreamStatusExecuting},
		domain.StreamStatusCompleted:  {domain.StreamStatusExecuting, domain.StreamStatusCompleted},
		domain.StreamStatusFailed:     {domain.StreamStatusExecuting, domain.StreamStatusFailed},
		domain.StreamStatusMergeReady: {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady},
		domain.StreamStatusMerging:    {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady, domain.StreamStatusMerging},
		domain.StreamStatusMerged:     {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady, domain.StreamStatusMerging, domain.StreamStatusMerged},
	}
	for _, step := range paths[target] {
		if err := store.UpdateStatus(ctx, id, step); err != nil {
			t.Fatalf("advancing stream to %s (step %s): %v", target, step, err)
		}
	}
}

func TestUpdateStatus_ValidTransition(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)
	store := NewStreamStore(d.Conn())

	stream := &domain.Stream{PlanID: plan.ID, Title: "s", FileScope: []string{"**"}, Dependencies: []string{}}
	store.Create(ctx, stream)

	if err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusExecuting); err != nil {
		t.Fatalf("pending → executing should succeed: %v", err)
	}
	got, _ := store.Get(ctx, stream.ID)
	if got.Status != domain.StreamStatusExecuting {
		t.Errorf("status = %q, want executing", got.Status)
	}
}

func TestUpdateStatus_InvalidTransition(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)
	store := NewStreamStore(d.Conn())

	stream := &domain.Stream{PlanID: plan.ID, Title: "s", FileScope: []string{"**"}, Dependencies: []string{}}
	store.Create(ctx, stream)

	err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusCompleted)
	if err == nil {
		t.Fatal("pending → completed should fail")
	}
	var ite *InvalidTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("expected InvalidTransitionError, got %T: %v", err, err)
	}
	if ite.From != domain.StreamStatusPending || ite.To != domain.StreamStatusCompleted {
		t.Errorf("error = %s → %s, want pending → completed", ite.From, ite.To)
	}
}

func TestUpdateStatus_TerminalState(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)
	store := NewStreamStore(d.Conn())

	stream := &domain.Stream{PlanID: plan.ID, Title: "s", FileScope: []string{"**"}, Dependencies: []string{}}
	store.Create(ctx, stream)
	advanceStream(t, store, ctx, stream.ID, domain.StreamStatusMerged)

	err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusPending)
	if err == nil {
		t.Fatal("merged → pending should fail")
	}
	var ite *InvalidTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("expected InvalidTransitionError, got %T: %v", err, err)
	}
}

func TestUpdateStatus_StreamNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewStreamStore(d.Conn())

	err := store.UpdateStatus(ctx, "nonexistent-id", domain.StreamStatusExecuting)
	if err == nil {
		t.Fatal("expected error for nonexistent stream")
	}
	var ite *InvalidTransitionError
	if errors.As(err, &ite) {
		t.Fatal("expected non-InvalidTransitionError for not-found stream")
	}
}

func TestUpdateStatus_RetryPath(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)
	store := NewStreamStore(d.Conn())

	stream := &domain.Stream{PlanID: plan.ID, Title: "s", FileScope: []string{"**"}, Dependencies: []string{}}
	store.Create(ctx, stream)
	advanceStream(t, store, ctx, stream.ID, domain.StreamStatusFailed)

	if err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusPending); err != nil {
		t.Fatalf("failed → pending should succeed: %v", err)
	}
}

func TestUpdateStatus_RecoveryPath(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test")
	plan := createTestPlan(t, NewPlanStore(d.Conn()), obj.ID)
	store := NewStreamStore(d.Conn())

	stream := &domain.Stream{PlanID: plan.ID, Title: "s", FileScope: []string{"**"}, Dependencies: []string{}}
	store.Create(ctx, stream)
	advanceStream(t, store, ctx, stream.ID, domain.StreamStatusMerging)

	if err := store.UpdateStatus(ctx, stream.ID, domain.StreamStatusMergeReady); err != nil {
		t.Fatalf("merging → merge_ready should succeed: %v", err)
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
	advanceStream(t, store, ctx, s1.ID, domain.StreamStatusCompleted)
	ready, err = store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (after s1 done): %v", err)
	}
	readyIDs = idsSet(ready)
	if readyIDs[s3.ID] {
		t.Error("s3 should not be ready when only s1 is completed")
	}

	// Mark s2 completed — s3 is still not ready until dependencies are merged.
	advanceStream(t, store, ctx, s2.ID, domain.StreamStatusCompleted)
	ready, err = store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (after s2 completed): %v", err)
	}
	readyIDs = idsSet(ready)
	if readyIDs[s3.ID] {
		t.Error("s3 should not be ready when dependencies are only completed")
	}

	for _, id := range []string{s1.ID, s2.ID} {
		if err := store.UpdateStatus(ctx, id, domain.StreamStatusMergeReady); err != nil {
			t.Fatalf("UpdateStatus(%s, merge_ready): %v", id, err)
		}
		if err := store.UpdateStatus(ctx, id, domain.StreamStatusMerging); err != nil {
			t.Fatalf("UpdateStatus(%s, merging): %v", id, err)
		}
		if err := store.UpdateStatus(ctx, id, domain.StreamStatusMerged); err != nil {
			t.Fatalf("UpdateStatus(%s, merged): %v", id, err)
		}
	}
	ready, err = store.ListReady(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListReady (after deps merged): %v", err)
	}
	readyIDs = idsSet(ready)
	if !readyIDs[s3.ID] {
		t.Error("s3 should be ready when both dependencies are merged")
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
