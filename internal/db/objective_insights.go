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

type ObjectiveInsightStore struct {
	db         *sql.DB
	candidates *CodificationCandidateStore
}

func NewObjectiveInsightStore(db *sql.DB) *ObjectiveInsightStore {
	return &ObjectiveInsightStore{db: db}
}

func (s *ObjectiveInsightStore) BindCodificationStore(candidates *CodificationCandidateStore) {
	s.candidates = candidates
}

func (s *ObjectiveInsightStore) Create(ctx context.Context, insight *domain.ObjectiveInsight) error {
	if insight.ProjectID == "" {
		projectID, err := projectIDForObjective(ctx, s.db, insight.ObjectiveID)
		if err != nil {
			projectID, err = defaultProjectID(ctx, s.db)
			if err != nil {
				return err
			}
		}
		insight.ProjectID = projectID
	}
	if insight.ID == "" {
		insight.ID = uuid.New().String()
	}
	if insight.CreatedAt.IsZero() {
		insight.CreatedAt = time.Now()
	}
	payload, err := json.Marshal(insight.Payload)
	if err != nil {
		return fmt.Errorf("marshalling insight payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO objective_insights (id, project_id, objective_id, stream_id, plan_id, execution_id, source, kind, summary, detail, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		insight.ID, insight.ProjectID, insight.ObjectiveID, insight.StreamID, insight.PlanID, insight.ExecutionID,
		string(insight.Source), string(insight.Kind), insight.Summary, insight.Detail, string(payload), insight.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting objective insight: %w", err)
	}
	if s.candidates != nil {
		insights, err := s.ListByObjective(ctx, insight.ObjectiveID, 0)
		if err != nil {
			return fmt.Errorf("refreshing codification candidates after insight insert: %w", err)
		}
		if err := s.candidates.RefreshForObjective(ctx, insight.ObjectiveID, insights); err != nil {
			return fmt.Errorf("refreshing codification candidates after insight insert: %w", err)
		}
	}
	return nil
}

func (s *ObjectiveInsightStore) ListByObjective(ctx context.Context, objectiveID string, limit int) ([]domain.ObjectiveInsight, error) {
	query := `SELECT id, project_id, objective_id, stream_id, plan_id, execution_id, source, kind, summary, detail, payload, created_at FROM objective_insights WHERE objective_id = ? ORDER BY created_at DESC, rowid DESC`
	args := []any{objectiveID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing objective insights: %w", err)
	}
	defer rows.Close()
	var insights []domain.ObjectiveInsight
	for rows.Next() {
		insight, err := scanObjectiveInsight(rows)
		if err != nil {
			return nil, err
		}
		insights = append(insights, insight)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating objective insights: %w", err)
	}
	return insights, nil
}

func scanObjectiveInsight(scanner interface{ Scan(dest ...any) error }) (domain.ObjectiveInsight, error) {
	var insight domain.ObjectiveInsight
	var source, kind, payload string
	var createdAt int64
	if err := scanner.Scan(
		&insight.ID,
		&insight.ProjectID,
		&insight.ObjectiveID,
		&insight.StreamID,
		&insight.PlanID,
		&insight.ExecutionID,
		&source,
		&kind,
		&insight.Summary,
		&insight.Detail,
		&payload,
		&createdAt,
	); err != nil {
		return domain.ObjectiveInsight{}, fmt.Errorf("scanning objective insight: %w", err)
	}
	insight.Source = domain.ObjectiveInsightSource(source)
	insight.Kind = domain.ObjectiveInsightKind(kind)
	if strings.TrimSpace(payload) != "" {
		if err := json.Unmarshal([]byte(payload), &insight.Payload); err != nil {
			return domain.ObjectiveInsight{}, fmt.Errorf("unmarshalling objective insight payload: %w", err)
		}
	}
	insight.CreatedAt = time.Unix(createdAt, 0)
	return insight, nil
}
