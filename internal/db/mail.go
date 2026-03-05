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

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO mail (from_agent, to_agent, type, payload, objective, stream, read, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		msg.From, msg.To, msg.Type, msg.Payload,
		msg.Objective, msg.Stream, now.Unix(),
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

// GetUnread returns all unread mail messages for a given agent, ordered by creation time ascending.
func (s *MailStore) GetUnread(ctx context.Context, agentName string) ([]domain.MailMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, from_agent, to_agent, type, payload, objective, stream, read, created_at
		 FROM mail WHERE to_agent = ? AND read = 0 ORDER BY created_at ASC`, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("querying unread mail: %w", err)
	}
	defer rows.Close()

	var messages []domain.MailMessage
	for rows.Next() {
		var msg domain.MailMessage
		var readInt int64
		var createdAt int64

		if err := rows.Scan(
			&msg.ID, &msg.From, &msg.To, &msg.Type, &msg.Payload,
			&msg.Objective, &msg.Stream, &readInt, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning mail message: %w", err)
		}

		msg.Read = readInt != 0
		msg.CreatedAt = time.Unix(createdAt, 0)
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating mail messages: %w", err)
	}
	return messages, nil
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
