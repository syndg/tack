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

// PlanStore persists plan records.
type PlanStore struct {
	db *sql.DB
}

// NewPlanStore creates a new PlanStore.
func NewPlanStore(db *sql.DB) *PlanStore {
	return &PlanStore{db: db}
}

// Create inserts a new plan. Generates UUID if ID is empty.
// Stores QualityGates as JSON text. Timestamps as Unix seconds.
func (s *PlanStore) Create(ctx context.Context, plan *domain.Plan) error {
	if plan.ID == "" {
		plan.ID = uuid.New().String()
	}
	if plan.Status == "" {
		plan.Status = domain.PlanStatusDraft
	}
	now := time.Now()
	plan.CreatedAt = now
	plan.UpdatedAt = now

	gates, err := json.Marshal(plan.QualityGates)
	if err != nil {
		return fmt.Errorf("marshaling quality gates: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO plans (id, objective_id, status, quality_gates, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.ObjectiveID, string(plan.Status), string(gates),
		now.Unix(), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting plan: %w", err)
	}
	return nil
}

// Get retrieves a plan by ID.
func (s *PlanStore) Get(ctx context.Context, id string) (*domain.Plan, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective_id, status, quality_gates, created_at, updated_at
		 FROM plans WHERE id = ?`, id,
	)

	var plan domain.Plan
	var status, gatesJSON string
	var createdAt, updatedAt int64

	err := row.Scan(&plan.ID, &plan.ObjectiveID, &status, &gatesJSON, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("plan not found: %s", id)
		}
		return nil, fmt.Errorf("getting plan %s: %w", id, err)
	}

	plan.Status = domain.PlanStatus(status)
	plan.CreatedAt = time.Unix(createdAt, 0)
	plan.UpdatedAt = time.Unix(updatedAt, 0)

	if err := json.Unmarshal([]byte(gatesJSON), &plan.QualityGates); err != nil {
		return nil, fmt.Errorf("unmarshaling quality gates: %w", err)
	}
	return &plan, nil
}

// GetByObjective retrieves the plan for a given objective.
func (s *PlanStore) GetByObjective(ctx context.Context, objectiveID string) (*domain.Plan, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective_id, status, quality_gates, created_at, updated_at
		 FROM plans WHERE objective_id = ? ORDER BY created_at DESC LIMIT 1`, objectiveID,
	)

	var plan domain.Plan
	var status, gatesJSON string
	var createdAt, updatedAt int64

	err := row.Scan(&plan.ID, &plan.ObjectiveID, &status, &gatesJSON, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("plan not found for objective: %s", objectiveID)
		}
		return nil, fmt.Errorf("getting plan for objective %s: %w", objectiveID, err)
	}

	plan.Status = domain.PlanStatus(status)
	plan.CreatedAt = time.Unix(createdAt, 0)
	plan.UpdatedAt = time.Unix(updatedAt, 0)

	if err := json.Unmarshal([]byte(gatesJSON), &plan.QualityGates); err != nil {
		return nil, fmt.Errorf("unmarshaling quality gates: %w", err)
	}
	return &plan, nil
}

// List returns all plans, ordered by created_at desc.
func (s *PlanStore) List(ctx context.Context) ([]domain.Plan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, objective_id, status, quality_gates, created_at, updated_at
		 FROM plans ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing plans: %w", err)
	}
	defer rows.Close()

	var plans []domain.Plan
	for rows.Next() {
		var plan domain.Plan
		var status, gatesJSON string
		var createdAt, updatedAt int64

		if err := rows.Scan(&plan.ID, &plan.ObjectiveID, &status, &gatesJSON, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning plan: %w", err)
		}

		plan.Status = domain.PlanStatus(status)
		plan.CreatedAt = time.Unix(createdAt, 0)
		plan.UpdatedAt = time.Unix(updatedAt, 0)

		if err := json.Unmarshal([]byte(gatesJSON), &plan.QualityGates); err != nil {
			return nil, fmt.Errorf("unmarshaling quality gates: %w", err)
		}
		plans = append(plans, plan)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating plans: %w", err)
	}
	return plans, nil
}

// UpdateStatus updates a plan's status and updated_at timestamp.
func (s *PlanStore) UpdateStatus(ctx context.Context, id string, status domain.PlanStatus) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE plans SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("updating plan status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("plan not found: %s", id)
	}
	return nil
}

// Update saves the full plan state (status, quality_gates, updated_at).
func (s *PlanStore) Update(ctx context.Context, plan *domain.Plan) error {
	plan.UpdatedAt = time.Now()

	gates, err := json.Marshal(plan.QualityGates)
	if err != nil {
		return fmt.Errorf("marshaling quality gates: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE plans SET status = ?, quality_gates = ?, updated_at = ? WHERE id = ?`,
		string(plan.Status), string(gates), plan.UpdatedAt.Unix(), plan.ID,
	)
	if err != nil {
		return fmt.Errorf("updating plan: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("plan not found: %s", plan.ID)
	}
	return nil
}
