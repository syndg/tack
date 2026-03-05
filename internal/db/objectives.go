package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/domain"
)

// ObjectiveStore handles persistence of objectives.
type ObjectiveStore struct {
	db *sql.DB
}

// NewObjectiveStore creates a new ObjectiveStore.
func NewObjectiveStore(db *sql.DB) *ObjectiveStore {
	return &ObjectiveStore{db: db}
}

// Create inserts a new objective. Generates a UUID if obj.ID is empty and
// defaults status to "planning" if empty.
func (s *ObjectiveStore) Create(ctx context.Context, obj *domain.Objective) error {
	if obj.ID == "" {
		obj.ID = uuid.New().String()
	}
	if obj.Status == "" {
		obj.Status = domain.ObjectiveStatusPlanning
	}
	now := time.Now()
	obj.CreatedAt = now
	obj.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO objectives (id, description, status, blueprint, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		obj.ID, obj.Description, string(obj.Status), obj.Blueprint,
		now.Unix(), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting objective: %w", err)
	}
	return nil
}

// Get retrieves an objective by ID. Returns a wrapped sql.ErrNoRows if not found.
func (s *ObjectiveStore) Get(ctx context.Context, id string) (*domain.Objective, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, description, status, blueprint, created_at, updated_at
		 FROM objectives WHERE id = ?`, id,
	)

	var obj domain.Objective
	var status string
	var createdAt, updatedAt int64

	err := row.Scan(&obj.ID, &obj.Description, &status, &obj.Blueprint, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting objective %s: %w", id, err)
	}

	obj.Status = domain.ObjectiveStatus(status)
	obj.CreatedAt = time.Unix(createdAt, 0)
	obj.UpdatedAt = time.Unix(updatedAt, 0)
	return &obj, nil
}

// List returns all objectives ordered by creation time descending.
func (s *ObjectiveStore) List(ctx context.Context) ([]domain.Objective, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, status, blueprint, created_at, updated_at
		 FROM objectives ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing objectives: %w", err)
	}
	defer rows.Close()

	var objectives []domain.Objective
	for rows.Next() {
		var obj domain.Objective
		var status string
		var createdAt, updatedAt int64

		if err := rows.Scan(&obj.ID, &obj.Description, &status, &obj.Blueprint, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning objective: %w", err)
		}

		obj.Status = domain.ObjectiveStatus(status)
		obj.CreatedAt = time.Unix(createdAt, 0)
		obj.UpdatedAt = time.Unix(updatedAt, 0)
		objectives = append(objectives, obj)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating objectives: %w", err)
	}
	return objectives, nil
}

// UpdateStatus changes the status of an objective and updates its timestamp.
func (s *ObjectiveStore) UpdateStatus(ctx context.Context, id string, status domain.ObjectiveStatus) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE objectives SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("updating objective status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating objective status: %w", sql.ErrNoRows)
	}
	return nil
}
