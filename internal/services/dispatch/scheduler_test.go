package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

func setupSchedulerTest(t *testing.T, maxConcurrent int) (*Scheduler, *db.StreamStore, *events.PersistentBus, string) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()
	objStore := db.NewObjectiveStore(d.Conn())
	planStore := db.NewPlanStore(d.Conn())
	streamStore := db.NewStreamStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())
	bus := events.NewPersistentBus(eventStore, slog.Default())
	sched := NewScheduler(streamStore, planStore, maxConcurrent, bus, slog.Default())

	obj := &domain.Objective{Description: "sched test"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	return sched, streamStore, bus, plan.ID
}

func TestGetReadyStreams_AllDepsCompleted(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 5)
	ctx := context.Background()

	// streamA: no dependencies → ready immediately
	streamA := &domain.Stream{PlanID: planID, Title: "A", Dependencies: []string{}}
	streamStore.Create(ctx, streamA)

	// streamB: depends on A which is pending → not ready
	streamB := &domain.Stream{PlanID: planID, Title: "B", Dependencies: []string{streamA.ID}}
	streamStore.Create(ctx, streamB)

	ready, err := sched.GetReadyStreams(ctx, planID)
	if err != nil {
		t.Fatalf("GetReadyStreams: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready stream, got %d", len(ready))
	}
	if ready[0].ID != streamA.ID {
		t.Errorf("ready stream = %q, want %q", ready[0].ID, streamA.ID)
	}
}

func TestGetReadyStreams_ExcludesUnsatisfiedDeps(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 5)
	ctx := context.Background()

	streamA := &domain.Stream{PlanID: planID, Title: "A", Dependencies: []string{}}
	streamStore.Create(ctx, streamA)

	// B depends on A which is "pending" — B should NOT be ready
	streamB := &domain.Stream{PlanID: planID, Title: "B", Dependencies: []string{streamA.ID}}
	streamStore.Create(ctx, streamB)

	ready, err := sched.GetReadyStreams(ctx, planID)
	if err != nil {
		t.Fatalf("GetReadyStreams: %v", err)
	}
	for _, s := range ready {
		if s.ID == streamB.ID {
			t.Error("streamB should not be ready: dependency not satisfied")
		}
	}
}

func TestGetReadyStreams_RespectsMaxConcurrentLimit(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 1)
	ctx := context.Background()

	// Two independent streams: both could run, but maxConcurrent=1
	for i := 0; i < 2; i++ {
		s := &domain.Stream{PlanID: planID, Title: fmt.Sprintf("S%d", i), Dependencies: []string{}}
		streamStore.Create(ctx, s)
	}

	ready, err := sched.GetReadyStreams(ctx, planID)
	if err != nil {
		t.Fatalf("GetReadyStreams: %v", err)
	}
	if len(ready) != 1 {
		t.Errorf("expected 1 ready stream (capped by maxConcurrent=1), got %d", len(ready))
	}
}

func TestMarkExecuting_UpdatesStreamStatusAndTracksInActiveSet(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 5)
	ctx := context.Background()

	stream := &domain.Stream{PlanID: planID, Title: "X", Dependencies: []string{}}
	streamStore.Create(ctx, stream)

	if err := sched.MarkExecuting(ctx, stream.ID); err != nil {
		t.Fatalf("MarkExecuting: %v", err)
	}

	if sched.ActiveCount() != 1 {
		t.Errorf("ActiveCount = %d, want 1", sched.ActiveCount())
	}

	got, err := streamStore.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if got.Status != "executing" {
		t.Errorf("stream status = %q, want executing", got.Status)
	}
}

func TestMarkCompleted_RemovesFromActiveSetAndPublishesCascadeEvent(t *testing.T) {
	sched, streamStore, bus, planID := setupSchedulerTest(t, 5)
	ctx := context.Background()

	streamA := &domain.Stream{PlanID: planID, Title: "A", Dependencies: []string{}}
	streamStore.Create(ctx, streamA)
	streamB := &domain.Stream{PlanID: planID, Title: "B", Dependencies: []string{streamA.ID}}
	streamStore.Create(ctx, streamB)

	sub, unsub := bus.Subscribe(10)
	defer unsub()

	sched.MarkExecuting(ctx, streamA.ID)

	if err := sched.MarkCompleted(ctx, streamA.ID, planID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	if sched.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0 after completing", sched.ActiveCount())
	}

	// Drain channel looking for EventStreamReady published for cascade (streamB)
	var gotReady bool
drain:
	for {
		select {
		case ev := <-sub:
			if ev.Type == domain.EventStreamReady {
				gotReady = true
			}
		default:
			break drain
		}
	}
	if !gotReady {
		t.Error("expected EventStreamReady to be published for newly unblocked stream")
	}
}

func TestMarkFailed_UpdatesStatusAndRemovesFromActiveSet(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 5)
	ctx := context.Background()

	stream := &domain.Stream{PlanID: planID, Title: "X", Dependencies: []string{}}
	streamStore.Create(ctx, stream)
	sched.MarkExecuting(ctx, stream.ID)

	if err := sched.MarkFailed(ctx, stream.ID); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if sched.ActiveCount() != 0 {
		t.Errorf("ActiveCount = %d, want 0 after failing", sched.ActiveCount())
	}

	got, err := streamStore.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("stream status = %q, want failed", got.Status)
	}
}

func TestCanScheduleMore_ReturnsCorrectResultBasedOnActiveCount(t *testing.T) {
	sched, streamStore, _, planID := setupSchedulerTest(t, 2)
	ctx := context.Background()

	if !sched.CanScheduleMore() {
		t.Error("CanScheduleMore should be true with no active streams")
	}

	// Fill to maxConcurrent
	for i := 0; i < 2; i++ {
		s := &domain.Stream{PlanID: planID, Title: fmt.Sprintf("S%d", i), Dependencies: []string{}}
		streamStore.Create(ctx, s)
		sched.MarkExecuting(ctx, s.ID)
	}

	if sched.CanScheduleMore() {
		t.Error("CanScheduleMore should be false at maxConcurrent")
	}
}
