package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

// InvalidTransitionError is returned when a stream status transition is not
// allowed by the state machine.
type InvalidTransitionError struct {
	StreamID string
	From     domain.StreamStatus
	To       domain.StreamStatus
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid stream transition: %s → %s (stream %s)", e.From, e.To, e.StreamID)
}

// validFromStatuses maps each target status to the set of statuses that can
// transition into it. Built from domain.IsValidStreamTransition at init time.
var validFromStatuses map[domain.StreamStatus][]domain.StreamStatus

func init() {
	allStatuses := []domain.StreamStatus{
		domain.StreamStatusPending,
		domain.StreamStatusExecuting,
		domain.StreamStatusCompleted,
		domain.StreamStatusFailed,
		domain.StreamStatusMergeReady,
		domain.StreamStatusMerging,
		domain.StreamStatusMerged,
	}
	validFromStatuses = make(map[domain.StreamStatus][]domain.StreamStatus)
	for _, to := range allStatuses {
		for _, from := range allStatuses {
			if domain.IsValidStreamTransition(from, to) {
				validFromStatuses[to] = append(validFromStatuses[to], from)
			}
		}
	}
}

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
	if stream.ProjectID == "" {
		projectID, err := projectIDForPlan(ctx, s.db, stream.PlanID)
		if err != nil {
			projectID, err = defaultProjectID(ctx, s.db)
			if err != nil {
				return err
			}
		}
		stream.ProjectID = projectID
	}
	if stream.ID == "" {
		stream.ID = uuid.New().String()
	}
	if stream.Status == "" {
		stream.Status = domain.StreamStatusPending
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
	description := domain.StreamDescriptionPayload(stream.Description, stream.AcceptanceCriteria)
	card, err := marshalStreamCard(stream.Card)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO streams (id, project_id, plan_id, title, description, stream_card, file_scope, dependencies, status, execution_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stream.ID, stream.ProjectID, stream.PlanID, stream.Title, description,
		card, string(fileScope), string(dependencies), stream.Status, stream.ExecutionID, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting stream: %w", err)
	}
	return nil
}

// Get retrieves a stream by ID.
func (s *StreamStore) Get(ctx context.Context, id string) (*domain.Stream, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, plan_id, title, description, stream_card, file_scope, dependencies, status, execution_id, created_at
		 FROM streams WHERE id = ?`, id,
	)

	var stream domain.Stream
	var fileScope, dependencies, card string
	var createdAt int64

	err := row.Scan(
		&stream.ID, &stream.ProjectID, &stream.PlanID, &stream.Title, &stream.Description,
		&card, &fileScope, &dependencies, &stream.Status, &stream.ExecutionID, &createdAt,
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
	stream.Description, stream.AcceptanceCriteria = domain.ParseStreamDescriptionPayload(stream.Description)
	stream.Card, err = unmarshalStreamCard(card)
	if err != nil {
		return nil, err
	}
	stream.CreatedAt = time.Unix(createdAt, 0)
	return &stream, nil
}

// ListByPlan returns all streams for a plan, ordered by created_at asc.
func (s *StreamStore) ListByPlan(ctx context.Context, planID string) ([]domain.Stream, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, plan_id, title, description, stream_card, file_scope, dependencies, status, execution_id, created_at
		 FROM streams WHERE plan_id = ? ORDER BY created_at ASC`, planID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing streams for plan %s: %w", planID, err)
	}
	defer rows.Close()

	var streams []domain.Stream
	for rows.Next() {
		var stream domain.Stream
		var fileScope, dependencies, card string
		var createdAt int64

		if err := rows.Scan(
			&stream.ID, &stream.ProjectID, &stream.PlanID, &stream.Title, &stream.Description,
			&card, &fileScope, &dependencies, &stream.Status, &stream.ExecutionID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning stream: %w", err)
		}

		if err := json.Unmarshal([]byte(fileScope), &stream.FileScope); err != nil {
			return nil, fmt.Errorf("unmarshalling file_scope: %w", err)
		}
		if err := json.Unmarshal([]byte(dependencies), &stream.Dependencies); err != nil {
			return nil, fmt.Errorf("unmarshalling dependencies: %w", err)
		}
		stream.Description, stream.AcceptanceCriteria = domain.ParseStreamDescriptionPayload(stream.Description)
		parsedCard, err := unmarshalStreamCard(card)
		if err != nil {
			return nil, err
		}
		stream.Card = parsedCard
		stream.CreatedAt = time.Unix(createdAt, 0)
		streams = append(streams, stream)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating streams: %w", err)
	}
	return streams, nil
}

// ListByProject returns streams for a single project ordered by creation time.
func (s *StreamStore) ListByProject(ctx context.Context, projectID string) ([]domain.Stream, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, plan_id, title, description, stream_card, file_scope, dependencies, status, execution_id, created_at
		 FROM streams WHERE project_id = ? ORDER BY created_at ASC`, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing streams for project %s: %w", projectID, err)
	}
	defer rows.Close()
	return scanStreams(rows)
}

// UpdateStatus atomically transitions a stream's status, enforcing valid
// transitions via a SQL WHERE clause. Returns InvalidTransitionError if the
// current status does not allow the requested transition.
func (s *StreamStore) UpdateStatus(ctx context.Context, id string, status domain.StreamStatus) error {
	fromStatuses := validFromStatuses[status]
	if len(fromStatuses) == 0 {
		return &InvalidTransitionError{StreamID: id, From: "unknown", To: status}
	}

	// Build WHERE status IN (?, ?, ...) clause.
	placeholders := make([]string, len(fromStatuses))
	args := make([]interface{}, 0, len(fromStatuses)+2)
	args = append(args, string(status))
	for i, fs := range fromStatuses {
		placeholders[i] = "?"
		args = append(args, string(fs))
	}
	args = append(args, id)

	query := fmt.Sprintf(
		`UPDATE streams SET status = ? WHERE status IN (%s) AND id = ?`,
		strings.Join(placeholders, ", "),
	)

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating stream status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n > 0 {
		return nil
	}

	// No rows affected — either stream not found or invalid transition.
	var currentStatus domain.StreamStatus
	err = s.db.QueryRowContext(ctx, `SELECT status FROM streams WHERE id = ?`, id).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("stream not found: %s", id)
		}
		return fmt.Errorf("checking stream status: %w", err)
	}

	return &InvalidTransitionError{StreamID: id, From: currentStatus, To: status}
}

func scanStreams(rows *sql.Rows) ([]domain.Stream, error) {
	var streams []domain.Stream
	for rows.Next() {
		var stream domain.Stream
		var fileScope string
		var dependencies string
		var card string
		var createdAt int64

		if err := rows.Scan(
			&stream.ID, &stream.ProjectID, &stream.PlanID, &stream.Title, &stream.Description,
			&card, &fileScope, &dependencies, &stream.Status, &stream.ExecutionID, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning stream: %w", err)
		}

		if err := json.Unmarshal([]byte(fileScope), &stream.FileScope); err != nil {
			return nil, fmt.Errorf("unmarshalling file_scope: %w", err)
		}
		if err := json.Unmarshal([]byte(dependencies), &stream.Dependencies); err != nil {
			return nil, fmt.Errorf("unmarshalling dependencies: %w", err)
		}
		stream.Description, stream.AcceptanceCriteria = domain.ParseStreamDescriptionPayload(stream.Description)
		parsedCard, err := unmarshalStreamCard(card)
		if err != nil {
			return nil, err
		}
		stream.Card = parsedCard
		stream.CreatedAt = time.Unix(createdAt, 0)
		streams = append(streams, stream)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating streams: %w", err)
	}
	return streams, nil
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
	description := domain.StreamDescriptionPayload(stream.Description, stream.AcceptanceCriteria)
	card, err := marshalStreamCard(stream.Card)
	if err != nil {
		return err
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE streams SET title = ?, description = ?, stream_card = ?, file_scope = ?, dependencies = ?, status = ? WHERE id = ?`,
		stream.Title, description, card, string(fileScope), string(dependencies), stream.Status, stream.ID,
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

// ListReady returns streams whose dependencies are all merged.
// A stream is ready if its status is "pending" and all stream IDs in its
// dependencies list have status "merged".
func (s *StreamStore) ListReady(ctx context.Context, planID string) ([]domain.Stream, error) {
	streams, err := s.ListByPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("listing streams for ready check: %w", err)
	}

	statusByID := make(map[string]domain.StreamStatus, len(streams))
	for _, st := range streams {
		statusByID[st.ID] = st.Status
	}

	var ready []domain.Stream
	for _, st := range streams {
		if st.Status != domain.StreamStatusPending {
			continue
		}
		allDone := true
		for _, depID := range st.Dependencies {
			depStatus := statusByID[depID]
			if depStatus != domain.StreamStatusMerged {
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

func marshalStreamCard(card *domain.StreamCard) (string, error) {
	if card == nil {
		return "", nil
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshalling stream_card: %w", err)
	}
	return string(raw), nil
}

func unmarshalStreamCard(raw string) (*domain.StreamCard, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var card domain.StreamCard
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		return nil, fmt.Errorf("unmarshalling stream_card: %w", err)
	}
	return &card, nil
}
