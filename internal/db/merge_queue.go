package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

// MergeQueueStore handles persistence of merge queue entries.
type MergeQueueStore struct {
	db *sql.DB
}

// NewMergeQueueStore creates a new MergeQueueStore.
func NewMergeQueueStore(db *sql.DB) *MergeQueueStore {
	return &MergeQueueStore{db: db}
}

// Enqueue adds a stream branch to the merge queue.
// Generates UUID, sets status to "pending", sets created_at/updated_at.
func (s *MergeQueueStore) Enqueue(ctx context.Context, entry *domain.MergeEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.Status == "" {
		entry.Status = domain.MergeStatusPending
	}
	now := time.Now().Unix()
	entry.CreatedAt = now
	entry.UpdatedAt = now

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO merge_queue (id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.StreamID, entry.PlanID, entry.ObjectiveID,
		entry.Branch, string(entry.Status), entry.Tier, entry.Error,
		entry.DiffStat, entry.CreatedAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueuing merge entry: %w", err)
	}
	return nil
}

// Dequeue returns the next pending entry (FIFO by created_at).
// Returns nil, nil if queue is empty.
func (s *MergeQueueStore) Dequeue(ctx context.Context) (*domain.MergeEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue WHERE status = ? ORDER BY created_at ASC LIMIT 1`,
		string(domain.MergeStatusPending),
	)

	entry, err := scanMergeEntry(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeuing merge entry: %w", err)
	}
	return entry, nil
}

// Get returns a merge entry by ID.
func (s *MergeQueueStore) Get(ctx context.Context, id string) (*domain.MergeEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue WHERE id = ?`, id,
	)

	entry, err := scanMergeEntry(row)
	if err != nil {
		return nil, fmt.Errorf("getting merge entry %s: %w", id, err)
	}
	return entry, nil
}

// GetByStream returns the merge entry for a stream (most recent).
func (s *MergeQueueStore) GetByStream(ctx context.Context, streamID string) (*domain.MergeEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue WHERE stream_id = ? ORDER BY created_at DESC LIMIT 1`, streamID,
	)

	entry, err := scanMergeEntry(row)
	if err != nil {
		return nil, fmt.Errorf("getting merge entry for stream %s: %w", streamID, err)
	}
	return entry, nil
}

// UpdateStatus updates the status, tier, error, and diff_stat of a merge entry.
func (s *MergeQueueStore) UpdateStatus(ctx context.Context, id string, status domain.MergeStatus, tier int, errMsg string, diffStat string) error {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE merge_queue SET status = ?, tier = ?, error = ?, diff_stat = ?, updated_at = ? WHERE id = ?`,
		string(status), tier, errMsg, diffStat, now, id,
	)
	if err != nil {
		return fmt.Errorf("updating merge entry status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updating merge entry status: %w", sql.ErrNoRows)
	}
	return nil
}

// ListByObjective returns all merge entries for an objective's streams.
func (s *MergeQueueStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.MergeEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue WHERE objective_id = ? ORDER BY created_at ASC`, objectiveID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing merge entries for objective %s: %w", objectiveID, err)
	}
	defer rows.Close()

	return scanMergeEntries(rows)
}

// ListAll returns all merge entries ordered by created_at (oldest first).
func (s *MergeQueueStore) ListAll(ctx context.Context) ([]domain.MergeEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all merge entries: %w", err)
	}
	defer rows.Close()

	return scanMergeEntries(rows)
}

// ListPending returns all pending entries ordered by created_at (FIFO).
func (s *MergeQueueStore) ListPending(ctx context.Context) ([]domain.MergeEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, stream_id, plan_id, objective_id, branch, status, tier, error, diff_stat, created_at, updated_at
		 FROM merge_queue WHERE status = ? ORDER BY created_at ASC`,
		string(domain.MergeStatusPending),
	)
	if err != nil {
		return nil, fmt.Errorf("listing pending merge entries: %w", err)
	}
	defer rows.Close()

	return scanMergeEntries(rows)
}

// CountPending returns the number of pending entries.
func (s *MergeQueueStore) CountPending(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM merge_queue WHERE status = ?`,
		string(domain.MergeStatusPending),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting pending merge entries: %w", err)
	}
	return count, nil
}

// UpdateMergerSandboxID sets the merger sandbox ID on all entries for an objective.
// Called when a merger sandbox is picked so it survives daemon restarts.
func (s *MergeQueueStore) UpdateMergerSandboxID(ctx context.Context, objectiveID, sandboxID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE merge_queue SET merger_sandbox_id = ? WHERE objective_id = ?`,
		sandboxID, objectiveID,
	)
	if err != nil {
		return fmt.Errorf("updating merger_sandbox_id for objective %s: %w", objectiveID, err)
	}
	return nil
}

// GetMergerSandboxID returns the persisted merger sandbox ID for an objective.
// Returns empty string if none is set.
func (s *MergeQueueStore) GetMergerSandboxID(ctx context.Context, objectiveID string) string {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT merger_sandbox_id FROM merge_queue WHERE objective_id = ? AND merger_sandbox_id != '' LIMIT 1`,
		objectiveID,
	).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// scanMergeEntry scans a single merge entry from a row.
type scannable interface {
	Scan(dest ...any) error
}

func scanMergeEntry(row scannable) (*domain.MergeEntry, error) {
	var entry domain.MergeEntry
	var status string

	err := row.Scan(
		&entry.ID, &entry.StreamID, &entry.PlanID, &entry.ObjectiveID,
		&entry.Branch, &status, &entry.Tier, &entry.Error,
		&entry.DiffStat, &entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	entry.Status = domain.MergeStatus(status)
	return &entry, nil
}

func scanMergeEntries(rows *sql.Rows) ([]domain.MergeEntry, error) {
	var entries []domain.MergeEntry
	for rows.Next() {
		entry, err := scanMergeEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning merge entry: %w", err)
		}
		entries = append(entries, *entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating merge entries: %w", err)
	}
	return entries, nil
}
