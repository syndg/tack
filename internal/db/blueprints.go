package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syndg/deck/internal/harness/blueprint"
)

// ExecutionStore persists blueprint execution state.
type ExecutionStore struct {
	db *sql.DB
}

// NewExecutionStore creates a new ExecutionStore.
func NewExecutionStore(db *sql.DB) *ExecutionStore {
	return &ExecutionStore{db: db}
}

// Create inserts a new execution record.
func (s *ExecutionStore) Create(ctx context.Context, exec *blueprint.Execution) error {
	stepStatesJSON, err := json.Marshal(exec.StepStates)
	if err != nil {
		return fmt.Errorf("marshaling step states: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO executions (id, blueprint_name, objective_id, current_step, step_states, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.BlueprintName, exec.ObjectiveID, exec.CurrentStep,
		string(stepStatesJSON), exec.Status,
		exec.CreatedAt.Unix(), exec.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting execution: %w", err)
	}
	return nil
}

// Get retrieves an execution by ID.
func (s *ExecutionStore) Get(ctx context.Context, id string) (*blueprint.Execution, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, blueprint_name, objective_id, current_step, step_states, status, created_at, updated_at
		 FROM executions WHERE id = ?`, id,
	)
	return s.scanExecution(row, id)
}

// GetByObjective retrieves the execution for a given objective.
func (s *ExecutionStore) GetByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, blueprint_name, objective_id, current_step, step_states, status, created_at, updated_at
		 FROM executions WHERE objective_id = ?`, objectiveID,
	)
	return s.scanExecution(row, objectiveID)
}

// scanExecution scans a single execution row.
func (s *ExecutionStore) scanExecution(row *sql.Row, ref string) (*blueprint.Execution, error) {
	var exec blueprint.Execution
	var stepStatesJSON string
	var createdAt, updatedAt int64

	err := row.Scan(
		&exec.ID, &exec.BlueprintName, &exec.ObjectiveID, &exec.CurrentStep,
		&stepStatesJSON, &exec.Status, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting execution %s: %w", ref, err)
	}

	if err := json.Unmarshal([]byte(stepStatesJSON), &exec.StepStates); err != nil {
		return nil, fmt.Errorf("unmarshaling step states for execution %s: %w", exec.ID, err)
	}

	exec.CreatedAt = time.Unix(createdAt, 0)
	exec.UpdatedAt = time.Unix(updatedAt, 0)
	return &exec, nil
}

// Update saves the current execution state (current_step, step_states JSON, status, updated_at).
func (s *ExecutionStore) Update(ctx context.Context, exec *blueprint.Execution) error {
	stepStatesJSON, err := json.Marshal(exec.StepStates)
	if err != nil {
		return fmt.Errorf("marshaling step states: %w", err)
	}

	exec.UpdatedAt = time.Now()

	result, err := s.db.ExecContext(ctx,
		`UPDATE executions SET current_step = ?, step_states = ?, status = ?, updated_at = ?
		 WHERE id = ?`,
		exec.CurrentStep, string(stepStatesJSON), exec.Status,
		exec.UpdatedAt.Unix(), exec.ID,
	)
	if err != nil {
		return fmt.Errorf("updating execution: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating execution: %w", sql.ErrNoRows)
	}
	return nil
}

// List returns all executions, ordered by created_at desc.
func (s *ExecutionStore) List(ctx context.Context) ([]blueprint.Execution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, blueprint_name, objective_id, current_step, step_states, status, created_at, updated_at
		 FROM executions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing executions: %w", err)
	}
	defer rows.Close()

	var executions []blueprint.Execution
	for rows.Next() {
		var exec blueprint.Execution
		var stepStatesJSON string
		var createdAt, updatedAt int64

		if err := rows.Scan(
			&exec.ID, &exec.BlueprintName, &exec.ObjectiveID, &exec.CurrentStep,
			&stepStatesJSON, &exec.Status, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning execution: %w", err)
		}

		if err := json.Unmarshal([]byte(stepStatesJSON), &exec.StepStates); err != nil {
			return nil, fmt.Errorf("unmarshaling step states for execution %s: %w", exec.ID, err)
		}

		exec.CreatedAt = time.Unix(createdAt, 0)
		exec.UpdatedAt = time.Unix(updatedAt, 0)
		executions = append(executions, exec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating executions: %w", err)
	}
	return executions, nil
}
