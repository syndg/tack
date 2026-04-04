package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syndg/tack/internal/harness/blueprint"
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
	if exec.ProjectID == "" {
		projectID, err := projectIDForObjective(ctx, s.db, exec.ObjectiveID)
		if err != nil {
			projectID, err = defaultProjectID(ctx, s.db)
			if err != nil {
				return err
			}
		}
		exec.ProjectID = projectID
	}
	stepStatesJSON, err := json.Marshal(exec.StepStates)
	if err != nil {
		return fmt.Errorf("marshaling step states: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO executions (id, project_id, blueprint_name, objective_id, current_step, step_states, status, parent_id, stream_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.ProjectID, exec.BlueprintID, exec.ObjectiveID, exec.CurrentStep,
		string(stepStatesJSON), exec.Status, exec.ParentID, exec.StreamID,
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
		`SELECT id, project_id, blueprint_name, objective_id, current_step, step_states, status, parent_id, stream_id, created_at, updated_at
		 FROM executions WHERE id = ?`, id,
	)
	return s.scanExecution(row, id)
}

// GetByObjective retrieves the top-level execution for a given objective.
func (s *ExecutionStore) GetByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, blueprint_name, objective_id, current_step, step_states, status, parent_id, stream_id, created_at, updated_at
		 FROM executions WHERE objective_id = ? AND parent_id = '' LIMIT 1`, objectiveID,
	)
	return s.scanExecution(row, objectiveID)
}

// ListByParent returns all sub-executions for a parent execution, ordered by created_at asc.
func (s *ExecutionStore) ListByParent(ctx context.Context, parentID string) ([]blueprint.Execution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, blueprint_name, objective_id, current_step, step_states, status, parent_id, stream_id, created_at, updated_at
		 FROM executions WHERE parent_id = ? ORDER BY created_at ASC`, parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sub-executions for parent %s: %w", parentID, err)
	}
	defer rows.Close()

	var executions []blueprint.Execution
	for rows.Next() {
		var exec blueprint.Execution
		var stepStatesJSON string
		var createdAt, updatedAt int64

		if err := rows.Scan(
			&exec.ID, &exec.ProjectID, &exec.BlueprintID, &exec.ObjectiveID, &exec.CurrentStep,
			&stepStatesJSON, &exec.Status, &exec.ParentID, &exec.StreamID, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning sub-execution: %w", err)
		}

		if err := json.Unmarshal([]byte(stepStatesJSON), &exec.StepStates); err != nil {
			return nil, fmt.Errorf("unmarshaling step states for execution %s: %w", exec.ID, err)
		}

		exec.CreatedAt = time.Unix(createdAt, 0)
		exec.UpdatedAt = time.Unix(updatedAt, 0)
		executions = append(executions, exec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sub-executions: %w", err)
	}
	return executions, nil
}

// scanExecution scans a single execution row.
func (s *ExecutionStore) scanExecution(row *sql.Row, ref string) (*blueprint.Execution, error) {
	var exec blueprint.Execution
	var stepStatesJSON string
	var createdAt, updatedAt int64

	err := row.Scan(
		&exec.ID, &exec.ProjectID, &exec.BlueprintID, &exec.ObjectiveID, &exec.CurrentStep,
		&stepStatesJSON, &exec.Status, &exec.ParentID, &exec.StreamID, &createdAt, &updatedAt,
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
	return s.list(ctx, "", false)
}

// ListByProject returns executions for a single project.
func (s *ExecutionStore) ListByProject(ctx context.Context, projectID string) ([]blueprint.Execution, error) {
	return s.list(ctx, projectID, true)
}

func (s *ExecutionStore) list(ctx context.Context, projectID string, filterByProject bool) ([]blueprint.Execution, error) {
	query := `SELECT id, project_id, blueprint_name, objective_id, current_step, step_states, status, parent_id, stream_id, created_at, updated_at FROM executions`
	args := []any{}
	if filterByProject {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx,
		query, args...,
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
			&exec.ID, &exec.ProjectID, &exec.BlueprintID, &exec.ObjectiveID, &exec.CurrentStep,
			&stepStatesJSON, &exec.Status, &exec.ParentID, &exec.StreamID, &createdAt, &updatedAt,
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
