package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

// RunStore persists run records.
type RunStore struct {
	db *sql.DB
}

// NewRunStore creates a new RunStore.
func NewRunStore(db *sql.DB) *RunStore {
	return &RunStore{db: db}
}

// Create inserts a new run. Generates a UUID if ID is empty and
// defaults status to "active".
func (s *RunStore) Create(ctx context.Context, run *domain.Run) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	if run.Status == "" {
		run.Status = domain.RunStatusActive
	}
	now := time.Now()
	run.CreatedAt = now
	run.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, objective_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		run.ID, run.ObjectiveID, string(run.Status),
		now.Unix(), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting run: %w", err)
	}
	return nil
}

// Get retrieves a run by ID.
func (s *RunStore) Get(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective_id, status, created_at, updated_at
		 FROM runs WHERE id = ?`, id,
	)

	var run domain.Run
	var status string
	var createdAt, updatedAt int64

	err := row.Scan(&run.ID, &run.ObjectiveID, &status, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, fmt.Errorf("getting run %s: %w", id, err)
	}

	run.Status = domain.RunStatus(status)
	run.CreatedAt = time.Unix(createdAt, 0)
	run.UpdatedAt = time.Unix(updatedAt, 0)
	return &run, nil
}

// GetByObjective retrieves the most recent run for a given objective.
func (s *RunStore) GetByObjective(ctx context.Context, objectiveID string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective_id, status, created_at, updated_at
		 FROM runs WHERE objective_id = ? ORDER BY created_at DESC LIMIT 1`, objectiveID,
	)

	var run domain.Run
	var status string
	var createdAt, updatedAt int64

	err := row.Scan(&run.ID, &run.ObjectiveID, &status, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found for objective: %s", objectiveID)
		}
		return nil, fmt.Errorf("getting run for objective %s: %w", objectiveID, err)
	}

	run.Status = domain.RunStatus(status)
	run.CreatedAt = time.Unix(createdAt, 0)
	run.UpdatedAt = time.Unix(updatedAt, 0)
	return &run, nil
}

// UpdateStatus changes the status of a run and updates its timestamp.
func (s *RunStore) UpdateStatus(ctx context.Context, id string, status domain.RunStatus) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("updating run status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating run status: %w", sql.ErrNoRows)
	}
	return nil
}

// ListActive returns all runs with non-terminal status, ordered by creation time.
func (s *RunStore) ListActive(ctx context.Context) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, objective_id, status, created_at, updated_at
		 FROM runs WHERE status IN ('active', 'blocked')
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing active runs: %w", err)
	}
	defer rows.Close()

	var runs []domain.Run
	for rows.Next() {
		var run domain.Run
		var status string
		var createdAt, updatedAt int64

		if err := rows.Scan(&run.ID, &run.ObjectiveID, &status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning run: %w", err)
		}

		run.Status = domain.RunStatus(status)
		run.CreatedAt = time.Unix(createdAt, 0)
		run.UpdatedAt = time.Unix(updatedAt, 0)
		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating runs: %w", err)
	}
	return runs, nil
}
