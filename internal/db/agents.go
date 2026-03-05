package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/domain"
)

// AgentStore handles persistence of agent sessions.
type AgentStore struct {
	db *sql.DB
}

// NewAgentStore creates a new AgentStore.
func NewAgentStore(db *sql.DB) *AgentStore {
	return &AgentStore{db: db}
}

// Create inserts a new agent session. Generates a UUID if session.ID is empty.
func (s *AgentStore) Create(ctx context.Context, session *domain.AgentSession) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	if session.Status == "" {
		session.Status = "pending"
	}
	now := time.Now()
	session.CreatedAt = now
	session.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_sessions (id, objective_id, stream_id, role, sandbox_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.ObjectiveID, session.StreamID,
		string(session.Role), session.SandboxID, session.Status,
		now.Unix(), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting agent session: %w", err)
	}
	return nil
}

// Get retrieves an agent session by ID. Returns a wrapped sql.ErrNoRows if not found.
func (s *AgentStore) Get(ctx context.Context, id string) (*domain.AgentSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective_id, stream_id, role, sandbox_id, status, created_at, updated_at
		 FROM agent_sessions WHERE id = ?`, id,
	)

	var session domain.AgentSession
	var role string
	var createdAt, updatedAt int64

	err := row.Scan(
		&session.ID, &session.ObjectiveID, &session.StreamID,
		&role, &session.SandboxID, &session.Status,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting agent session %s: %w", id, err)
	}

	session.Role = domain.AgentRole(role)
	session.CreatedAt = time.Unix(createdAt, 0)
	session.UpdatedAt = time.Unix(updatedAt, 0)
	return &session, nil
}

// ListByObjective returns all agent sessions for a given objective, ordered by creation time descending.
func (s *AgentStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.AgentSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, objective_id, stream_id, role, sandbox_id, status, created_at, updated_at
		 FROM agent_sessions WHERE objective_id = ? ORDER BY created_at DESC`, objectiveID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing agent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []domain.AgentSession
	for rows.Next() {
		var session domain.AgentSession
		var role string
		var createdAt, updatedAt int64

		if err := rows.Scan(
			&session.ID, &session.ObjectiveID, &session.StreamID,
			&role, &session.SandboxID, &session.Status,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning agent session: %w", err)
		}

		session.Role = domain.AgentRole(role)
		session.CreatedAt = time.Unix(createdAt, 0)
		session.UpdatedAt = time.Unix(updatedAt, 0)
		sessions = append(sessions, session)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent sessions: %w", err)
	}
	return sessions, nil
}

// UpdateStatus changes the status of an agent session and updates its timestamp.
func (s *AgentStore) UpdateStatus(ctx context.Context, id string, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE agent_sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("updating agent session status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating agent session status: %w", sql.ErrNoRows)
	}
	return nil
}
