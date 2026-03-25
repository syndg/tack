package mail

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	events "github.com/syndg/tack/internal/services/events"
)

type brokerFixture struct {
	broker     *Broker
	agentStore *db.AgentStore
	bus        *events.PersistentBus
}

func setupBroker(t *testing.T) *brokerFixture {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	mailStore := db.NewMailStore(d.Conn())
	agentStore := db.NewAgentStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())
	bus := events.NewPersistentBus(eventStore, slog.Default())
	broker := New(mailStore, agentStore, bus, slog.Default())

	return &brokerFixture{broker: broker, agentStore: agentStore, bus: bus}
}

func TestSend_PersistedAndEventPublished(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	msg := &domain.MailMessage{
		From: "alice", To: "bob", Subject: "hello", Body: "hi there",
		Type: "message", Objective: "obj-1",
	}
	if err := f.broker.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected msg.ID to be set after Send")
	}

	select {
	case ev := <-sub:
		if ev.Type != domain.EventMailSent {
			t.Errorf("event type = %q, want %q", ev.Type, domain.EventMailSent)
		}
	default:
		t.Error("expected EventMailSent to be published")
	}

	msgs, err := f.broker.GetUnread(ctx, "bob")
	if err != nil {
		t.Fatalf("GetUnread: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 unread message, got %d", len(msgs))
	}
	if msgs[0].Subject != "hello" {
		t.Errorf("Subject = %q, want hello", msgs[0].Subject)
	}
}

func TestHumanEscalation_PublishesEventAndStores(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	msg := &domain.MailMessage{
		From: "builder", To: "@human", Subject: "help", Body: "need help",
		Type: "escalation", Objective: "obj-1", Stream: "stream-1",
	}
	if err := f.broker.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should publish escalation event
	select {
	case ev := <-sub:
		if ev.Type != domain.EventEscalation {
			t.Errorf("event type = %q, want %q", ev.Type, domain.EventEscalation)
		}
	default:
		t.Error("expected EventEscalation to be published")
	}

	// Escalation should be stored for audit trail
	if msg.ID == 0 {
		t.Error("expected escalation to be stored with an ID")
	}
}

func TestEscalationDedup_SkipsDuplicate(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	// Send first escalation
	msg1 := &domain.MailMessage{
		From: "builder", To: "@human", Subject: "TS errors blocking gate", Body: "details",
		Type: "escalation", Objective: "obj-1", Stream: "stream-1",
	}
	if err := f.broker.Send(ctx, msg1); err != nil {
		t.Fatalf("Send first: %v", err)
	}
	// Drain the event
	<-sub

	// Send identical escalation — should be skipped
	msg2 := &domain.MailMessage{
		From: "builder", To: "@human", Subject: "TS errors blocking gate", Body: "details again",
		Type: "escalation", Objective: "obj-1", Stream: "stream-1",
	}
	if err := f.broker.Send(ctx, msg2); err != nil {
		t.Fatalf("Send second: %v", err)
	}

	// Should NOT have published a second event
	select {
	case ev := <-sub:
		t.Errorf("expected no second event, got %q", ev.Type)
	default:
		// Good — no duplicate event
	}

	// msg2 should not have been stored (ID remains 0)
	if msg2.ID != 0 {
		t.Error("expected duplicate escalation to not be stored")
	}
}

func TestEscalationDedup_AllowsAfterRead(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	// Send first escalation
	msg1 := &domain.MailMessage{
		From: "builder", To: "@human", Subject: "blocker", Body: "help",
		Type: "escalation", Objective: "obj-1", Stream: "stream-1",
	}
	f.broker.Send(ctx, msg1)
	<-sub // drain event

	// Mark as read (human has seen it)
	f.broker.MarkRead(ctx, msg1.ID)

	// Send same escalation again — should go through since the first was read
	msg2 := &domain.MailMessage{
		From: "builder", To: "@human", Subject: "blocker", Body: "still blocked",
		Type: "escalation", Objective: "obj-1", Stream: "stream-1",
	}
	if err := f.broker.Send(ctx, msg2); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Type != domain.EventEscalation {
			t.Errorf("event type = %q, want escalation", ev.Type)
		}
	default:
		t.Error("expected escalation event after read")
	}
}

func TestGetUnread_ReturnsOnlyUnread(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	var firstID int64
	for i := 0; i < 3; i++ {
		msg := &domain.MailMessage{
			From: "s", To: "receiver", Subject: "test", Body: "x",
			Type: "message", Objective: "obj-1",
		}
		f.broker.Send(ctx, msg)
		if i == 0 {
			firstID = msg.ID
		}
	}
	f.broker.MarkRead(ctx, firstID)

	unread, err := f.broker.GetUnread(ctx, "receiver")
	if err != nil {
		t.Fatalf("GetUnread: %v", err)
	}
	if len(unread) != 2 {
		t.Errorf("expected 2 unread, got %d", len(unread))
	}
}

func TestMarkRead_UpdatesReadStatus(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	msg := &domain.MailMessage{
		From: "a", To: "b", Subject: "test", Body: "x",
		Type: "message", Objective: "obj-1",
	}
	f.broker.Send(ctx, msg)

	if err := f.broker.MarkRead(ctx, msg.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	unread, _ := f.broker.GetUnread(ctx, "b")
	if len(unread) != 0 {
		t.Errorf("expected 0 unread after MarkRead, got %d", len(unread))
	}
}

func TestMarkAllRead_MarksAllAgentMessages(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		msg := &domain.MailMessage{
			From: "a", To: "all-agent", Subject: "test", Body: "x",
			Type: "message", Objective: "obj-1",
		}
		f.broker.Send(ctx, msg)
	}

	if err := f.broker.MarkAllRead(ctx, "all-agent"); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}

	unread, _ := f.broker.GetUnread(ctx, "all-agent")
	if len(unread) != 0 {
		t.Errorf("expected 0 unread after MarkAllRead, got %d", len(unread))
	}
}

func TestGetUnread_PriorityOrdering(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	priorities := []string{"low", "normal", "high", "urgent"}
	for _, p := range priorities {
		msg := &domain.MailMessage{
			From: "s", To: "r", Subject: p, Body: p,
			Type: "message", Priority: p, Objective: "obj-1",
		}
		f.broker.Send(ctx, msg)
	}

	msgs, _ := f.broker.GetUnread(ctx, "r")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	expected := []string{"urgent", "high", "normal", "low"}
	for i, exp := range expected {
		if msgs[i].Priority != exp {
			t.Errorf("msgs[%d].Priority = %q, want %q", i, msgs[i].Priority, exp)
		}
	}
}
