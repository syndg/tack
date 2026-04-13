package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syndg/tack/internal/domain"
)

type DossierStore struct {
	db *sql.DB
}

func NewDossierStore(db *sql.DB) *DossierStore {
	return &DossierStore{db: db}
}

func (s *DossierStore) GetByObjective(ctx context.Context, objectiveID string) (*domain.Dossier, error) {
	row := s.db.QueryRowContext(ctx, `SELECT project_id, content, created_at, updated_at FROM dossiers WHERE objective_id = ?`, objectiveID)
	var (
		projectID string
		content   string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&projectID, &content, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("getting dossier for objective %s: %w", objectiveID, err)
	}
	var dossier domain.Dossier
	if err := json.Unmarshal([]byte(content), &dossier); err != nil {
		return nil, fmt.Errorf("decoding dossier for objective %s: %w", objectiveID, err)
	}
	dossier.ObjectiveID = objectiveID
	dossier.ProjectID = projectID
	dossier.CreatedAt = time.Unix(createdAt, 0)
	dossier.UpdatedAt = time.Unix(updatedAt, 0)
	return &dossier, nil
}

func (s *DossierStore) Upsert(ctx context.Context, dossier *domain.Dossier) error {
	if dossier.ObjectiveID == "" {
		return fmt.Errorf("upserting dossier: objective_id is required")
	}
	if dossier.ProjectID == "" {
		projectID, err := projectIDForObjective(ctx, s.db, dossier.ObjectiveID)
		if err != nil {
			return err
		}
		dossier.ProjectID = projectID
	}
	now := time.Now()
	if existing, err := s.GetByObjective(ctx, dossier.ObjectiveID); err == nil {
		dossier.CreatedAt = existing.CreatedAt
	} else {
		dossier.CreatedAt = now
	}
	dossier.UpdatedAt = now
	content, err := json.Marshal(dossier)
	if err != nil {
		return fmt.Errorf("encoding dossier for objective %s: %w", dossier.ObjectiveID, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO dossiers (objective_id, project_id, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(objective_id) DO UPDATE SET
			project_id = excluded.project_id,
			content = excluded.content,
			updated_at = excluded.updated_at
	`, dossier.ObjectiveID, dossier.ProjectID, string(content), dossier.CreatedAt.Unix(), dossier.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("upserting dossier for objective %s: %w", dossier.ObjectiveID, err)
	}
	return nil
}
