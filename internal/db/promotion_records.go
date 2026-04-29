package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

type PromotionRecordStore struct {
	db *sql.DB
}

func NewPromotionRecordStore(db *sql.DB) *PromotionRecordStore {
	return &PromotionRecordStore{db: db}
}

func (s *PromotionRecordStore) Create(ctx context.Context, record *domain.PromotionRecord) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}
	now := time.Now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	sourceInsightIDs, err := json.Marshal(record.SourceInsightIDs)
	if err != nil {
		return fmt.Errorf("marshalling promotion source insight ids: %w", err)
	}
	payload, err := json.Marshal(record.Payload)
	if err != nil {
		return fmt.Errorf("marshalling promotion payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO promotion_records (id, project_id, objective_id, source_candidate_id, source_insight_ids, target, status, confidence, support_count, summary, detail, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.ProjectID, record.ObjectiveID, record.SourceCandidateID, string(sourceInsightIDs), string(record.Target), string(record.Status), record.Confidence, record.SupportCount, record.Summary, record.Detail, string(payload), record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("inserting promotion record: %w", err)
	}
	return nil
}

func (s *PromotionRecordStore) UpdateStatus(ctx context.Context, id string, status domain.PromotionStatus) (*domain.PromotionRecord, error) {
	now := time.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE promotion_records SET status = ?, updated_at = ? WHERE id = ?`, string(status), now.Unix(), id)
	if err != nil {
		return nil, fmt.Errorf("updating promotion status: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil, sql.ErrNoRows
	}
	return s.Get(ctx, id)
}

func (s *PromotionRecordStore) Get(ctx context.Context, id string) (*domain.PromotionRecord, error) {
	row := s.db.QueryRowContext(ctx, selectPromotionRecordSQL()+` WHERE id = ?`, id)
	record, err := scanPromotionRecord(row)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PromotionRecordStore) GetByCandidateAndTarget(ctx context.Context, projectID, candidateID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	row := s.db.QueryRowContext(ctx, selectPromotionRecordSQL()+` WHERE project_id = ? AND source_candidate_id = ? AND target = ?`, projectID, candidateID, string(target))
	record, err := scanPromotionRecord(row)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PromotionRecordStore) GetByInsightAndTarget(ctx context.Context, projectID, insightID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	sourceInsightIDs, err := json.Marshal([]string{insightID})
	if err != nil {
		return nil, fmt.Errorf("marshalling promotion source insight ids: %w", err)
	}
	row := s.db.QueryRowContext(ctx, selectPromotionRecordSQL()+` WHERE project_id = ? AND source_candidate_id = '' AND source_insight_ids = ? AND target = ?`, projectID, string(sourceInsightIDs), string(target))
	record, err := scanPromotionRecord(row)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PromotionRecordStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.PromotionRecord, error) {
	rows, err := s.db.QueryContext(ctx, selectPromotionRecordSQL()+` WHERE objective_id = ? ORDER BY updated_at DESC, rowid DESC`, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("listing promotion records: %w", err)
	}
	defer rows.Close()
	var out []domain.PromotionRecord
	for rows.Next() {
		record, err := scanPromotionRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating promotion records: %w", err)
	}
	return out, nil
}

func selectPromotionRecordSQL() string {
	return `SELECT id, project_id, objective_id, source_candidate_id, source_insight_ids, target, status, confidence, support_count, summary, detail, payload, created_at, updated_at FROM promotion_records`
}

func scanPromotionRecord(scanner interface{ Scan(dest ...any) error }) (domain.PromotionRecord, error) {
	var record domain.PromotionRecord
	var sourceInsightIDs, target, status, payload string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&record.ID, &record.ProjectID, &record.ObjectiveID, &record.SourceCandidateID, &sourceInsightIDs, &target, &status, &record.Confidence, &record.SupportCount, &record.Summary, &record.Detail, &payload, &createdAt, &updatedAt); err != nil {
		return domain.PromotionRecord{}, fmt.Errorf("scanning promotion record: %w", err)
	}
	if sourceInsightIDs != "" {
		if err := json.Unmarshal([]byte(sourceInsightIDs), &record.SourceInsightIDs); err != nil {
			return domain.PromotionRecord{}, fmt.Errorf("unmarshalling promotion source insight ids: %w", err)
		}
	}
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &record.Payload); err != nil {
			return domain.PromotionRecord{}, fmt.Errorf("unmarshalling promotion payload: %w", err)
		}
	}
	record.Target = domain.PromotionTarget(target)
	record.Status = domain.PromotionStatus(status)
	record.CreatedAt = time.Unix(createdAt, 0)
	record.UpdatedAt = time.Unix(updatedAt, 0)
	return record, nil
}
