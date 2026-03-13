package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/domain"
)

// StreamStore persists stream records.
type StreamStore struct {
	db *sql.DB
}

// NewStreamStore creates a new StreamStore.
func NewStreamStore(db *sql.DB) *StreamStore {
	return &StreamStore{db: db}
}

// Create inserts a new stream. Generates UUID if ID is empty.
// Stores FileScope and Dependencies as JSON text.
func (s *StreamStore) Create(ctx context.Context, stream *domain.Stream) error {
	if stream.ID == "" {
		stream.ID = uuid.New().String()
	}
	if stream.Status == "" {
		stream.Status = "pending"
	}
	now := time.Now()
	stream.CreatedAt = now

	fileScope, err := json.Marshal(stream.FileScope)
	if err != nil {
		return fmt.Errorf("marshalling file_scope: %w", err)
	}
	dependencies, err := json.Marshal(stream.Dependencies)
	if err != nil {
		return fmt.Errorf("marshalling dependencies: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO streams (id, plan_id, title, description, file_scope, dependencies, status, execution_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stream.ID, stream.PlanID, stream.Title, stream.Description,
		string(fileScope), string(dependencies), stream.Status, stream.ExecutionID, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting stream: %w", err)
	}
	return nil
}

// Get retrieves a stream by ID.
func (s *StreamStore) Get(ctx context.Context, id string) (*domain.Stream, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, plan_id, title, description, file_scope, dependencies, status, execution_id, created_at
		 FROM streams WHERE id = ?`, id,
	)

	var stream domain.Stream
	var fileScope, dependencies string
	var createdAt int64

	err := row.Scan(
		&stream.ID, &stream.PlanID, &stream.Title, &stream.Description,
		&fileScope, &dependencies, &stream.Status, &stream.ExecutionID, &createdAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("stream not found: %s", id)
		}
		return nil, fmt.Errorf("getting stream %s: %w", id, err)
	}

	if err := json.Unmarshal([]byte(fileScope), &stream.FileScope); err != nil {
		return nil, fmt.Errorf("unmarshalling file_scope: %w", err)
	}
	if err := json.Unmarshal([]byte(dependencies), &stream.Dependencies); err != nil {
		return nil, fmt.Errorf("unmarshalling dependencies: %w", err)
	}
	stream.CreatedAt = time.Unix(createdAt, 0)
	return &stream, nil
}

// ListByPlan returns all streams for a plan, ordered by created_at asc.
func (s *StreamStore) ListByPlan(ctx context.Context, planID string) ([]domain.Stream, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, plan_id, title, description, file_scope, dependencies, status, execution_id, created_at
		 FROM streams WHERE plan_id = ? ORDER BY created_at ASC`, planID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing streams for plan %s: %w", planID, err)
	}
	defer rows.Close()

	var streams []domain.Stream
	for rows.Next() {
		var stream domain.Stream
		var fileScope, dependencies string
		var createdAt int64

		if err := rows.Scan(
			&stream.ID, &stream.PlanID, &stream.Title, &stream.Description,
			&fileScope, &dependencies, &stream.Status, &stream.ExecutionID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning stream: %w", err)
		}

		if err := json.Unmarshal([]byte(fileScope), &stream.FileScope); err != nil {
			return nil, fmt.Errorf("unmarshalling file_scope: %w", err)
		}
		if err := json.Unmarshal([]byte(dependencies), &stream.Dependencies); err != nil {
			return nil, fmt.Errorf("unmarshalling dependencies: %w", err)
		}
		stream.CreatedAt = time.Unix(createdAt, 0)
		streams = append(streams, stream)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating streams: %w", err)
	}
	return streams, nil
}

// UpdateStatus updates a stream's status.
func (s *StreamStore) UpdateStatus(ctx context.Context, id string, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE streams SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("updating stream status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating stream status: %w", sql.ErrNoRows)
	}
	return nil
}

// Update saves the mutable fields of a stream (title, description, file_scope,
// dependencies, status). Used for plan editing before approval.
func (s *StreamStore) Update(ctx context.Context, stream *domain.Stream) error {
	fileScope, err := json.Marshal(stream.FileScope)
	if err != nil {
		return fmt.Errorf("marshalling file_scope: %w", err)
	}
	dependencies, err := json.Marshal(stream.Dependencies)
	if err != nil {
		return fmt.Errorf("marshalling dependencies: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE streams SET title = ?, description = ?, file_scope = ?, dependencies = ?, status = ? WHERE id = ?`,
		stream.Title, stream.Description, string(fileScope), string(dependencies), stream.Status, stream.ID,
	)
	if err != nil {
		return fmt.Errorf("updating stream: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("stream not found: %s", stream.ID)
	}
	return nil
}

// UpdateExecutionID sets the sub-execution ID for a stream.
func (s *StreamStore) UpdateExecutionID(ctx context.Context, id string, executionID string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE streams SET execution_id = ? WHERE id = ?`,
		executionID, id,
	)
	if err != nil {
		return fmt.Errorf("updating stream execution_id: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("stream not found: %s", id)
	}
	return nil
}

// ListReady returns streams whose dependencies are all completed.
// A stream is ready if its status is "pending" and all stream IDs in its
// dependencies list have status "completed".
func (s *StreamStore) ListReady(ctx context.Context, planID string) ([]domain.Stream, error) {
	streams, err := s.ListByPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("listing streams for ready check: %w", err)
	}

	statusByID := make(map[string]string, len(streams))
	for _, st := range streams {
		statusByID[st.ID] = st.Status
	}

	var ready []domain.Stream
	for _, st := range streams {
		if st.Status != "pending" {
			continue
		}
		allDone := true
		for _, depID := range st.Dependencies {
			depStatus := statusByID[depID]
			if depStatus != "completed" && depStatus != "merge_ready" && depStatus != "merged" {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, st)
		}
	}
	return ready, nil
}
