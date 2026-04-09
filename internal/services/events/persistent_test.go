package events

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

func TestPersistentBusShutdownDisablesPersistenceButKeepsBroadcast(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.NewProjectStore(database.Conn()).Upsert(context.Background(), &domain.Project{ID: "test-project", Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir() + "/.tack/config.yaml"}); err != nil {
		t.Fatalf("register test project: %v", err)
	}

	store := db.NewEventStore(database.Conn())
	bus := NewPersistentBus(store, slog.Default())
	sub, unsub := bus.Subscribe(2)
	defer unsub()

	bus.Publish(domain.Event{ProjectID: "test-project", Type: domain.EventObjectiveCreated, Objective: "obj-1"})
	select {
	case ev := <-sub:
		if ev.Type != domain.EventObjectiveCreated {
			t.Fatalf("first broadcast event type = %q, want %q", ev.Type, domain.EventObjectiveCreated)
		}
	case <-time.After(time.Second):
		t.Fatal("expected initial broadcast event")
	}
	stored, err := store.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent before shutdown: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored events before shutdown = %d, want 1", len(stored))
	}

	bus.Shutdown()
	bus.Publish(domain.Event{ProjectID: "test-project", Type: domain.EventRecoveryAttempt, Objective: "obj-1"})

	select {
	case ev := <-sub:
		if ev.Type != domain.EventRecoveryAttempt {
			t.Fatalf("broadcast event type = %q, want %q", ev.Type, domain.EventRecoveryAttempt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected recovery attempt broadcast after shutdown")
	}

	stored, err = store.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent after shutdown: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored events after shutdown = %d, want 1", len(stored))
	}
}
