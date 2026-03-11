package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

// Broker manages inter-agent mail delivery and broadcast resolution.
type Broker struct {
	mail     *db.MailStore
	agents   *db.AgentStore
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
		agents:   agents,
		eventBus: eventBus,
		logger:   logger,
	}
}

// Send delivers a message from one agent to another.
// If msg.To is a broadcast address (starts with "@"), delegates to SendBroadcast.
// Publishes EventMailSent after persisting.
func (b *Broker) Send(ctx context.Context, msg *domain.MailMessage) error {
	if IsBroadcast(msg.To) {
		return b.SendBroadcast(ctx, msg.From, msg.To, msg.Type, msg.Payload, msg.Objective, msg.Stream)
	}

	if err := b.mail.Send(ctx, msg); err != nil {
		return fmt.Errorf("sending mail: %w", err)
	}

	b.eventBus.Publish(domain.Event{
		Type:      domain.EventMailSent,
		Objective: msg.Objective,
		Stream:    msg.Stream,
		Agent:     msg.From,
		Payload:   fmt.Sprintf(`{"to":%q,"type":%q,"count":1}`, msg.To, msg.Type),
	})

	return nil
}

// SendBroadcast resolves a broadcast address and delivers to all matching agents.
// Broadcast addresses:
//
//	@all           — all active agents for the objective
//	@stream:{id}   — all agents assigned to the given stream
//	@builders      — all agents with role "worker" (builder sub-role)
//	@leads         — all agents with role "lead"
//	@human         — special: publish EventEscalation, do not deliver to agents
func (b *Broker) SendBroadcast(ctx context.Context, from, broadcastAddr, msgType, payload, objectiveID, streamID string) error {
	// @human is a special case: escalate and do not deliver to agents.
	if broadcastAddr == "@human" {
		b.eventBus.Publish(domain.Event{
			Type:      domain.EventEscalation,
			Objective: objectiveID,
			Stream:    streamID,
			Agent:     from,
			Payload:   payload,
		})
		return nil
	}

	sessions, err := b.agents.ListByObjective(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("listing agents for objective %s: %w", objectiveID, err)
	}

	var recipients []domain.AgentSession
	for _, s := range sessions {
		switch {
		case broadcastAddr == "@all":
			recipients = append(recipients, s)
		case strings.HasPrefix(broadcastAddr, "@stream:"):
			targetStreamID := strings.TrimPrefix(broadcastAddr, "@stream:")
			if s.StreamID == targetStreamID {
				recipients = append(recipients, s)
			}
		case broadcastAddr == "@builders":
			if s.Role == domain.AgentRoleWorker {
				recipients = append(recipients, s)
			}
		case broadcastAddr == "@leads":
			if s.Role == domain.AgentRoleLead {
				recipients = append(recipients, s)
			}
		}
	}

	for _, recipient := range recipients {
		msg := &domain.MailMessage{
			From:      from,
			To:        recipient.ID,
			Type:      msgType,
			Payload:   payload,
			Objective: objectiveID,
			Stream:    streamID,
		}
		if err := b.mail.Send(ctx, msg); err != nil {
			b.logger.Error("failed to send broadcast mail", "to", recipient.ID, "broadcast", broadcastAddr, "error", err)
		}
	}

	b.eventBus.Publish(domain.Event{
		Type:      domain.EventMailSent,
		Objective: objectiveID,
		Stream:    streamID,
		Agent:     from,
		Payload:   fmt.Sprintf(`{"to":%q,"type":%q,"count":%d}`, broadcastAddr, msgType, len(recipients)),
	})

	return nil
}

// GetUnread retrieves unread messages for an agent, ordered by creation time.
// Delegates to MailStore.GetUnread().
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
	messages, err := b.mail.GetUnread(ctx, agentName)
	if err != nil {
		return fmt.Errorf("getting unread messages for %s: %w", agentName, err)
	}
	for _, msg := range messages {
		if err := b.mail.MarkRead(ctx, msg.ID); err != nil {
			return fmt.Errorf("marking message %d as read: %w", msg.ID, err)
		}
	}
	return nil
}

// IsBroadcast returns true if the address is a broadcast group (@all, @leads, etc).
func IsBroadcast(addr string) bool {
	return strings.HasPrefix(addr, "@")
}
