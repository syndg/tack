package mail

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

// Broker manages escalation delivery and mail storage.
// Agent-to-agent messaging has been removed — with isolated worktrees,
// agents cannot act on each other's messages. The broker now handles:
//   - Agent-to-human escalation (@human)
//   - Mail storage for audit trail and escalation history
//   - Escalation dedup (prevents identical repeated escalations)
type Broker struct {
	mail     *db.MailStore
	eventBus *events.PersistentBus
	logger   *slog.Logger
}

func New(
	mail *db.MailStore,
	agents *db.AgentStore,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Broker {
	return &Broker{
		mail:     mail,
		eventBus: eventBus,
		logger:   logger,
	}
}

// Send delivers a message. @human triggers an escalation event.
// All other messages are stored for audit trail.
func (b *Broker) Send(ctx context.Context, msg *domain.MailMessage) error {
	// @human: publish escalation event, store for audit trail
	if msg.To == "@human" {
		// Escalation dedup: skip if an unread escalation with the same
		// subject+stream already exists.
		dedupKey := computeDedupKey(msg.Subject, msg.Stream)
		msg.DedupKey = dedupKey

		exists, err := b.mail.ExistsUnreadDedup(ctx, dedupKey)
		if err != nil {
			b.logger.Error("checking escalation dedup", "error", err)
			// Continue anyway — better to duplicate than to drop
		} else if exists {
			b.logger.Info("skipping duplicate escalation",
				"subject", msg.Subject,
				"stream", msg.Stream,
			)
			return nil
		}

		// Store the escalation for audit trail
		if err := b.mail.Send(ctx, msg); err != nil {
			return fmt.Errorf("storing escalation: %w", err)
		}

		b.eventBus.Publish(domain.Event{
			Type:      domain.EventEscalation,
			Objective: msg.Objective,
			Stream:    msg.Stream,
			Agent:     msg.From,
			Payload:   msg.Body,
		})
		return nil
	}

	// All other messages: store for audit trail
	if err := b.mail.Send(ctx, msg); err != nil {
		return fmt.Errorf("sending mail: %w", err)
	}

	b.eventBus.Publish(domain.Event{
		Type:      domain.EventMailSent,
		Objective: msg.Objective,
		Stream:    msg.Stream,
		Agent:     msg.From,
		Payload:   fmt.Sprintf(`{"to":%q,"type":%q}`, msg.To, msg.Type),
	})

	return nil
}

// GetUnread retrieves unread messages for an agent.
func (b *Broker) GetUnread(ctx context.Context, agentName string) ([]domain.MailMessage, error) {
	messages, err := b.mail.GetUnread(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("getting unread mail for %s: %w", agentName, err)
	}
	return messages, nil
}

// MarkRead marks a single message as read.
func (b *Broker) MarkRead(ctx context.Context, messageID int64) error {
	if err := b.mail.MarkRead(ctx, messageID); err != nil {
		return fmt.Errorf("marking message %d as read: %w", messageID, err)
	}
	return nil
}

// MarkAllRead marks all unread messages for an agent as read.
func (b *Broker) MarkAllRead(ctx context.Context, agentName string) error {
	if _, err := b.mail.MarkAllRead(ctx, agentName); err != nil {
		return fmt.Errorf("marking all messages for %s as read: %w", agentName, err)
	}
	return nil
}

// List returns messages matching the given filters.
func (b *Broker) List(ctx context.Context, filters db.MailFilters) ([]domain.MailMessage, error) {
	messages, err := b.mail.List(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("listing mail: %w", err)
	}
	return messages, nil
}

// computeDedupKey creates a deterministic key for escalation dedup.
func computeDedupKey(subject, streamID string) string {
	h := sha256.Sum256([]byte(subject + "\x00" + streamID))
	return fmt.Sprintf("%x", h[:16])
}
