package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

// AttemptStore persists recovery attempt records.
type AttemptStore struct {
	db *sql.DB
}

// NewAttemptStore creates a new AttemptStore.
func NewAttemptStore(db *sql.DB) *AttemptStore {
	return &AttemptStore{db: db}
}

// Create appends a recovery attempt record.
func (s *AttemptStore) Create(ctx context.Context, attempt *domain.Attempt) error {
	if attempt.ProjectID == "" {
		projectID, err := projectIDForObjective(ctx, s.db, attempt.ObjectiveID)
		if err != nil {
			projectID, err = defaultProjectID(ctx, s.db)
			if err != nil {
				return err
			}
		}
		attempt.ProjectID = projectID
	}
	if attempt.ID == "" {
		attempt.ID = uuid.New().String()
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO attempts (
			id, project_id, objective_id, run_id, execution_id, stream_id, step_id,
			merge_entry_id, attempt_number, max_attempts, failure_kind, action, status,
			error_summary, fix_context, human_guidance, triggered_by_attempt_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.ID,
		attempt.ProjectID,
		attempt.ObjectiveID,
		attempt.RunID,
		attempt.ExecutionID,
		attempt.StreamID,
		attempt.StepID,
		attempt.MergeEntryID,
		attempt.AttemptNumber,
		attempt.MaxAttempts,
		string(attempt.FailureKind),
		string(attempt.Action),
		string(attempt.Status),
		attempt.ErrorSummary,
		attempt.FixContext,
		attempt.HumanGuidance,
		attempt.TriggeredByAttempt,
		attempt.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting attempt: %w", err)
	}
	return nil
}

// ListByExecution returns all attempts for an execution ordered by created_at asc.
func (s *AttemptStore) ListByExecution(ctx context.Context, executionID string) ([]domain.Attempt, error) {
	return s.list(ctx, `SELECT id, project_id, objective_id, run_id, execution_id, stream_id, step_id, merge_entry_id, attempt_number, max_attempts, failure_kind, action, status, error_summary, fix_context, human_guidance, triggered_by_attempt_id, created_at FROM attempts WHERE execution_id = ? ORDER BY created_at ASC, rowid ASC`, executionID)
}

// ListByStream returns all attempts for a stream ordered by created_at asc.
func (s *AttemptStore) ListByStream(ctx context.Context, streamID string) ([]domain.Attempt, error) {
	return s.list(ctx, `SELECT id, project_id, objective_id, run_id, execution_id, stream_id, step_id, merge_entry_id, attempt_number, max_attempts, failure_kind, action, status, error_summary, fix_context, human_guidance, triggered_by_attempt_id, created_at FROM attempts WHERE stream_id = ? ORDER BY created_at ASC, rowid ASC`, streamID)
}

// ListByRun returns all attempts for a run ordered by created_at asc.
func (s *AttemptStore) ListByRun(ctx context.Context, runID string) ([]domain.Attempt, error) {
	return s.list(ctx, `SELECT id, project_id, objective_id, run_id, execution_id, stream_id, step_id, merge_entry_id, attempt_number, max_attempts, failure_kind, action, status, error_summary, fix_context, human_guidance, triggered_by_attempt_id, created_at FROM attempts WHERE run_id = ? ORDER BY created_at ASC, rowid ASC`, runID)
}

func (s *AttemptStore) list(ctx context.Context, query string, arg string) ([]domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("listing attempts: %w", err)
	}
	defer rows.Close()

	var attempts []domain.Attempt
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating attempts: %w", err)
	}
	return attempts, nil
}

func scanAttempt(scanner interface{ Scan(dest ...any) error }) (domain.Attempt, error) {
	var attempt domain.Attempt
	var failureKind, action, status string
	var createdAt int64
	if err := scanner.Scan(
		&attempt.ID,
		&attempt.ProjectID,
		&attempt.ObjectiveID,
		&attempt.RunID,
		&attempt.ExecutionID,
		&attempt.StreamID,
		&attempt.StepID,
		&attempt.MergeEntryID,
		&attempt.AttemptNumber,
		&attempt.MaxAttempts,
		&failureKind,
		&action,
		&status,
		&attempt.ErrorSummary,
		&attempt.FixContext,
		&attempt.HumanGuidance,
		&attempt.TriggeredByAttempt,
		&createdAt,
	); err != nil {
		return domain.Attempt{}, fmt.Errorf("scanning attempt: %w", err)
	}
	attempt.FailureKind = domain.FailureKind(failureKind)
	attempt.Action = domain.RecoveryAction(action)
	attempt.Status = domain.AttemptStatus(status)
	attempt.CreatedAt = time.Unix(createdAt, 0)
	return attempt, nil
}
