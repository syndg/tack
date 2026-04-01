package runs

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

// openTestDB opens a fresh SQLite DB with migrations applied.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return d
}

func TestSnapshotCreateAndLoad(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, logger)

	// Create an objective.
	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}

	// Create a plan for the objective.
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("creating plan: %v", err)
	}

	// Create streams for the plan.
	streamA := &domain.Stream{PlanID: plan.ID, Title: "stream-a", Description: "first stream"}
	streamB := &domain.Stream{PlanID: plan.ID, Title: "stream-b", Description: "second stream"}
	if err := streamStore.Create(ctx, streamA); err != nil {
		t.Fatalf("creating stream A: %v", err)
	}
	if err := streamStore.Create(ctx, streamB); err != nil {
		t.Fatalf("creating stream B: %v", err)
	}

	// Create a run for the objective.
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Load snapshot and verify it reflects persisted state.
	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if snap.RunID != run.ID {
		t.Errorf("RunID = %q, want %q", snap.RunID, run.ID)
	}
	if snap.ObjectiveID != obj.ID {
		t.Errorf("ObjectiveID = %q, want %q", snap.ObjectiveID, obj.ID)
	}
	if snap.Status != domain.RunStatusActive {
		t.Errorf("Status = %q, want %q", snap.Status, domain.RunStatusActive)
	}
	if snap.Blocked != nil {
		t.Errorf("Blocked = %v, want nil", snap.Blocked)
	}
	if snap.Outcome != nil {
		t.Errorf("Outcome = %v, want nil", snap.Outcome)
	}
	if len(snap.Streams) != 2 {
		t.Fatalf("len(Streams) = %d, want 2", len(snap.Streams))
	}
	if snap.Streams[0].Title != "stream-a" {
		t.Errorf("Streams[0].Title = %q, want %q", snap.Streams[0].Title, "stream-a")
	}
	if snap.Streams[1].Title != "stream-b" {
		t.Errorf("Streams[1].Title = %q, want %q", snap.Streams[1].Title, "stream-b")
	}
	if snap.Streams[0].Status != domain.StreamStatusPending {
		t.Errorf("Streams[0].Status = %q, want %q", snap.Streams[0].Status, domain.StreamStatusPending)
	}
}

func TestSnapshotByObjective(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, logger)

	// Create objective + run.
	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	snap, err := svc.SnapshotByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("SnapshotByObjective: %v", err)
	}
	if snap.RunID != run.ID {
		t.Errorf("RunID = %q, want %q", snap.RunID, run.ID)
	}
}

func TestSnapshotTerminalOutcome(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, logger)

	obj := &domain.Objective{Description: "test objective"}
	if err := objectiveStore.Create(ctx, obj); err != nil {
		t.Fatalf("creating objective: %v", err)
	}
	run := &domain.Run{ObjectiveID: obj.ID}
	if err := runStore.Create(ctx, run); err != nil {
		t.Fatalf("creating run: %v", err)
	}

	// Transition run to completed.
	if err := runStore.UpdateStatus(ctx, run.ID, domain.RunStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	snap, err := svc.Snapshot(ctx, run.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Status != domain.RunStatusCompleted {
		t.Errorf("Status = %q, want %q", snap.Status, domain.RunStatusCompleted)
	}
	if snap.Outcome == nil {
		t.Fatal("Outcome is nil, want non-nil")
	}
	if snap.Outcome.Status != domain.RunStatusCompleted {
		t.Errorf("Outcome.Status = %q, want %q", snap.Outcome.Status, domain.RunStatusCompleted)
	}
}

func TestSnapshotNotFound(t *testing.T) {
	database := openTestDB(t)
	conn := database.Conn()
	ctx := context.Background()
	logger := slog.Default()

	runStore := db.NewRunStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	executionStore := db.NewExecutionStore(conn)

	svc := New(runStore, objectiveStore, planStore, streamStore, executionStore, logger)

	_, err := svc.Snapshot(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent run, got nil")
	}
}
