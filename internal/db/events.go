package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/syndg/deck/internal/domain"
)

// EventStore handles persistence of system events.
type EventStore struct {
	db *sql.DB
}

// NewEventStore creates a new EventStore.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db}
}

// Insert stores a new event in the events table.
func (s *EventStore) Insert(ctx context.Context, event *domain.Event) error {
	now := time.Now()
	event.CreatedAt = now

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO events (type, objective, stream, agent, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		string(event.Type), event.Objective, event.Stream,
		event.Agent, event.Payload, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting event insert id: %w", err)
	}
	event.ID = id
	return nil
}

// ListByObjective returns events for a given objective, ordered by creation time descending.
func (s *EventStore) ListByObjective(ctx context.Context, objectiveID string, limit int) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, objective, stream, agent, payload, created_at
		 FROM events WHERE objective = ? ORDER BY created_at DESC LIMIT ?`,
		objectiveID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying events by objective: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// ListRecent returns the most recent events across all objectives.
func (s *EventStore) ListRecent(ctx context.Context, limit int) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, objective, stream, agent, payload, created_at
		 FROM events ORDER BY created_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying recent events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]domain.Event, error) {
	var events []domain.Event
	for rows.Next() {
		var ev domain.Event
		var eventType string
		var createdAt int64

		if err := rows.Scan(
			&ev.ID, &eventType, &ev.Objective, &ev.Stream,
			&ev.Agent, &ev.Payload, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}

		ev.Type = domain.EventType(eventType)
		ev.CreatedAt = time.Unix(createdAt, 0)
		events = append(events, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}
