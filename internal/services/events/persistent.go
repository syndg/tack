package events

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

// PersistentBus wraps Bus to persist events to the database before broadcasting.
type PersistentBus struct {
	*Bus
	store           *db.EventStore
	persistDisabled atomic.Bool
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
	if pb.store != nil && !pb.persistDisabled.Load() {
		if err := pb.store.Insert(context.Background(), &event); err != nil {
			if isExpectedEventPersistenceError(err) {
				pb.logger.Debug("skipping event persistence during shutdown", "type", event.Type, "error", err)
			} else {
				pb.logger.Error("failed to persist event", "type", event.Type, "error", err)
			}
		}
	}

	pb.Bus.Publish(event)
}

// Shutdown disables event persistence while keeping in-memory delivery active.
func (pb *PersistentBus) Shutdown() {
	pb.persistDisabled.Store(true)
}

func isExpectedEventPersistenceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "database is closed")
}
