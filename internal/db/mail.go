package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/syndg/deck/internal/domain"
)

// MailStore handles persistence of inter-agent mail messages.
type MailStore struct {
	db *sql.DB
}

// NewMailStore creates a new MailStore.
func NewMailStore(db *sql.DB) *MailStore {
	return &MailStore{db: db}
}

// Send inserts a new mail message into the mail table.
func (s *MailStore) Send(ctx context.Context, msg *domain.MailMessage) error {
	now := time.Now()
	msg.CreatedAt = now
	if msg.Priority == "" {
		msg.Priority = string(domain.MailPriorityNormal)
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO mail (from_agent, to_agent, subject, body, type, priority, thread_id, payload, objective, stream, read, dedup_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		msg.From, msg.To, msg.Subject, msg.Body, msg.Type, msg.Priority, msg.ThreadID,
		msg.Payload, msg.Objective, msg.Stream, msg.DedupKey, now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("inserting mail message: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting mail insert id: %w", err)
	}
	msg.ID = id
	return nil
}

// ExistsUnreadDedup checks if an unread message with the given dedup key exists.
func (s *MailStore) ExistsUnreadDedup(ctx context.Context, dedupKey string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail WHERE dedup_key = ? AND read = 0`, dedupKey,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking dedup key: %w", err)
	}
	return count > 0, nil
}

const mailColumns = `id, from_agent, to_agent, subject, body, type, priority, thread_id, payload, dedup_key, objective, stream, read, created_at`

func scanMailMessage(rows *sql.Rows) (domain.MailMessage, error) {
	var msg domain.MailMessage
	var readInt int64
	var createdAt int64

	if err := rows.Scan(
		&msg.ID, &msg.From, &msg.To, &msg.Subject, &msg.Body,
		&msg.Type, &msg.Priority, &msg.ThreadID, &msg.Payload,
		&msg.DedupKey, &msg.Objective, &msg.Stream, &readInt, &createdAt,
	); err != nil {
		return msg, fmt.Errorf("scanning mail message: %w", err)
	}

	msg.Read = readInt != 0
	msg.CreatedAt = time.Unix(createdAt, 0)
	return msg, nil
}

// GetUnread returns all unread mail messages for a given agent.
// Orders by priority (urgent first) then creation time ascending.
func (s *MailStore) GetUnread(ctx context.Context, agentName string) ([]domain.MailMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mailColumns+`
		 FROM mail WHERE to_agent = ? AND read = 0
		 ORDER BY
		   CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
		   created_at ASC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("querying unread mail: %w", err)
	}
	defer rows.Close()

	var messages []domain.MailMessage
	for rows.Next() {
		msg, err := scanMailMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating mail messages: %w", err)
	}
	return messages, nil
}

// GetByID returns a single mail message by its numeric ID.
func (s *MailStore) GetByID(ctx context.Context, id int64) (*domain.MailMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+mailColumns+` FROM mail WHERE id = ?`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("querying mail by id: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	msg, err := scanMailMessage(rows)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// MarkRead marks a mail message as read.
func (s *MailStore) MarkRead(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE mail SET read = 1 WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("marking mail as read: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("marking mail as read: %w", sql.ErrNoRows)
	}
	return nil
}

// MarkAllRead marks all unread messages for an agent as read.
func (s *MailStore) MarkAllRead(ctx context.Context, agentName string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE mail SET read = 1 WHERE to_agent = ? AND read = 0`, agentName,
	)
	if err != nil {
		return 0, fmt.Errorf("marking all mail as read: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}
	return n, nil
}

// List returns mail messages matching the provided filters.
func (s *MailStore) List(ctx context.Context, filters MailFilters) ([]domain.MailMessage, error) {
	query := `SELECT ` + mailColumns + ` FROM mail WHERE 1=1`
	var args []any

	if filters.Objective != "" {
		query += ` AND objective = ?`
		args = append(args, filters.Objective)
	}
	if filters.From != "" {
		query += ` AND from_agent = ?`
		args = append(args, filters.From)
	}
	if filters.To != "" {
		query += ` AND to_agent = ?`
		args = append(args, filters.To)
	}
	if filters.Type != "" {
		query += ` AND type = ?`
		args = append(args, filters.Type)
	}
	if filters.UnreadOnly {
		query += ` AND read = 0`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing mail: %w", err)
	}
	defer rows.Close()

	var messages []domain.MailMessage
	for rows.Next() {
		msg, err := scanMailMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating mail list: %w", err)
	}
	return messages, nil
}

// MailFilters contains optional filters for listing mail.
type MailFilters struct {
	Objective  string
	From       string
	To         string
	Type       string
	UnreadOnly bool
}
