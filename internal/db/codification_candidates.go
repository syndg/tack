package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syndg/tack/internal/codification"
	"github.com/syndg/tack/internal/domain"
)

type CodificationCandidateStore struct {
	db *sql.DB
}

func NewCodificationCandidateStore(db *sql.DB) *CodificationCandidateStore {
	return &CodificationCandidateStore{db: db}
}

func (s *CodificationCandidateStore) RefreshForObjective(ctx context.Context, objectiveID string, insights []domain.ObjectiveInsight) error {
	projectID, err := projectIDForObjective(ctx, s.db, objectiveID)
	if err != nil {
		projectID, err = defaultProjectID(ctx, s.db)
		if err != nil {
			return err
		}
	}
	candidates := codification.DeriveCandidates(projectID, objectiveID, insights)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin refresh codification candidates: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM codification_candidates WHERE objective_id = ?`, objectiveID); err != nil {
		return fmt.Errorf("clearing codification candidates: %w", err)
	}
	for _, candidate := range candidates {
		payload, err := json.Marshal(candidate.Payload)
		if err != nil {
			return fmt.Errorf("marshalling codification candidate payload: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO codification_candidates (id, project_id, objective_id, status, target, title, instruction, rationale, evidence_count, payload, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			candidate.ID, candidate.ProjectID, candidate.ObjectiveID, string(candidate.Status), string(candidate.Target), candidate.Title, candidate.Instruction, candidate.Rationale, candidate.EvidenceCount, string(payload), candidate.CreatedAt.Unix(), candidate.UpdatedAt.Unix(),
		); err != nil {
			return fmt.Errorf("inserting codification candidate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refresh codification candidates: %w", err)
	}
	return nil
}

func (s *CodificationCandidateStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.CodificationCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project_id, objective_id, status, target, title, instruction, rationale, evidence_count, payload, created_at, updated_at FROM codification_candidates WHERE objective_id = ? ORDER BY evidence_count DESC, updated_at DESC, rowid DESC`, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("listing codification candidates: %w", err)
	}
	defer rows.Close()
	var out []domain.CodificationCandidate
	for rows.Next() {
		candidate, err := scanCodificationCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating codification candidates: %w", err)
	}
	return out, nil
}

func scanCodificationCandidate(scanner interface{ Scan(dest ...any) error }) (domain.CodificationCandidate, error) {
	var candidate domain.CodificationCandidate
	var status, target, payload string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&candidate.ID, &candidate.ProjectID, &candidate.ObjectiveID, &status, &target, &candidate.Title, &candidate.Instruction, &candidate.Rationale, &candidate.EvidenceCount, &payload, &createdAt, &updatedAt); err != nil {
		return domain.CodificationCandidate{}, fmt.Errorf("scanning codification candidate: %w", err)
	}
	candidate.Status = domain.CodificationCandidateStatus(status)
	candidate.Target = domain.CodificationCandidateTarget(target)
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &candidate.Payload); err != nil {
			return domain.CodificationCandidate{}, fmt.Errorf("unmarshalling codification candidate payload: %w", err)
		}
	}
	candidate.CreatedAt = time.Unix(createdAt, 0)
	candidate.UpdatedAt = time.Unix(updatedAt, 0)
	return candidate, nil
}
