package events

import (
	"context"
	"log/slog"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
)

// PersistentBus wraps Bus to persist events to the database before broadcasting.
type PersistentBus struct {
	*Bus
	store *db.EventStore
}

// NewPersistentBus creates a bus that persists events via the EventStore
// and then broadcasts them to in-memory subscribers.
func NewPersistentBus(store *db.EventStore, logger *slog.Logger) *PersistentBus {
	return &PersistentBus{
		Bus:   NewBus(logger),
		store: store,
	}
}

// Publish persists the event to the database and then broadcasts it
// to all subscribers. Database errors are logged but do not prevent broadcasting.
func (pb *PersistentBus) Publish(event domain.Event) {
	if pb.store != nil {
		if err := pb.store.Insert(context.Background(), &event); err != nil {
			pb.logger.Error("failed to persist event", "type", event.Type, "error", err)
		}
	}

	pb.Bus.Publish(event)
}
