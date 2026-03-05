package events

import (
	"log/slog"
	"sync"

	"github.com/syndg/deck/internal/domain"
)

// Subscriber is a channel that receives events from the bus.
type Subscriber chan domain.Event

// Bus is an in-memory pub/sub event bus with thread-safe subscriber management.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[int]Subscriber
	nextID      int
	logger      *slog.Logger
}

// NewBus creates a new event bus.
func NewBus(logger *slog.Logger) *Bus {
	return &Bus{
		subscribers: make(map[int]Subscriber),
		logger:      logger,
	}
}

// Publish sends an event to all subscribers. Non-blocking: if a subscriber's
// buffer is full, the event is dropped for that subscriber.
func (b *Bus) Publish(event domain.Event) {
	b.logger.Info("event published", "type", event.Type, "objective", event.Objective)

	b.mu.RLock()
	defer b.mu.RUnlock()

	for id, sub := range b.subscribers {
		select {
		case sub <- event:
		default:
			b.logger.Warn("dropping event for slow subscriber", "subscriber_id", id, "type", event.Type)
		}
	}
}

// Subscribe registers a new subscriber with the given buffer size.
// Returns the subscriber channel and an unsubscribe function.
func (b *Bus) Subscribe(buffer int) (Subscriber, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	sub := make(Subscriber, buffer)
	b.subscribers[id] = sub

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subscribers, id)
		close(sub)
	}

	return sub, unsubscribe
}
