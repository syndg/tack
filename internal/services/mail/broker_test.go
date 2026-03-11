package mail

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
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
		From: "alice", To: "bob", Type: "note", Payload: "hello", Objective: "obj-1",
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
	if msgs[0].From != "alice" {
		t.Errorf("From = %q, want alice", msgs[0].From)
	}
}

func TestSendBroadcast_All(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	objID := "obj-all"
	roles := []domain.AgentRole{domain.AgentRoleLead, domain.AgentRoleWorker, domain.AgentRolePlanner}
	var sessions []*domain.AgentSession
	for _, role := range roles {
		sess := &domain.AgentSession{ObjectiveID: objID, Role: role}
		if err := f.agentStore.Create(ctx, sess); err != nil {
			t.Fatalf("Create agent: %v", err)
		}
		sessions = append(sessions, sess)
	}

	if err := f.broker.SendBroadcast(ctx, "sender", "@all", "note", "hi", objID, ""); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}

	for _, sess := range sessions {
		msgs, err := f.broker.GetUnread(ctx, sess.ID)
		if err != nil {
			t.Fatalf("GetUnread(%s): %v", sess.ID, err)
		}
		if len(msgs) != 1 {
			t.Errorf("agent %s (%s): expected 1 message, got %d", sess.ID, sess.Role, len(msgs))
		}
	}
}

func TestSendBroadcast_Stream(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	objID := "obj-stream"
	targetStream := "stream-abc"
	otherStream := "stream-xyz"

	inStream := &domain.AgentSession{ObjectiveID: objID, Role: domain.AgentRoleWorker, StreamID: targetStream}
	outOfStream := &domain.AgentSession{ObjectiveID: objID, Role: domain.AgentRoleWorker, StreamID: otherStream}
	f.agentStore.Create(ctx, inStream)
	f.agentStore.Create(ctx, outOfStream)

	if err := f.broker.SendBroadcast(ctx, "sender", "@stream:"+targetStream, "note", "hi", objID, targetStream); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}

	in, _ := f.broker.GetUnread(ctx, inStream.ID)
	if len(in) != 1 {
		t.Errorf("in-stream agent: expected 1 message, got %d", len(in))
	}
	out, _ := f.broker.GetUnread(ctx, outOfStream.ID)
	if len(out) != 0 {
		t.Errorf("out-of-stream agent: expected 0 messages, got %d", len(out))
	}
}

func TestSendBroadcast_Leads(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	objID := "obj-leads"
	lead := &domain.AgentSession{ObjectiveID: objID, Role: domain.AgentRoleLead}
	worker := &domain.AgentSession{ObjectiveID: objID, Role: domain.AgentRoleWorker}
	f.agentStore.Create(ctx, lead)
	f.agentStore.Create(ctx, worker)

	if err := f.broker.SendBroadcast(ctx, "sender", "@leads", "note", "hi", objID, ""); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}

	leadMsgs, _ := f.broker.GetUnread(ctx, lead.ID)
	if len(leadMsgs) != 1 {
		t.Errorf("lead: expected 1 message, got %d", len(leadMsgs))
	}
	workerMsgs, _ := f.broker.GetUnread(ctx, worker.ID)
	if len(workerMsgs) != 0 {
		t.Errorf("worker: expected 0 messages (leads only), got %d", len(workerMsgs))
	}
}

func TestSendBroadcast_Human_PublishesEscalationWithoutAgentDelivery(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	objID := "obj-human"
	agent := &domain.AgentSession{ObjectiveID: objID, Role: domain.AgentRoleWorker}
	f.agentStore.Create(ctx, agent)

	if err := f.broker.SendBroadcast(ctx, "sender", "@human", "question", "help?", objID, ""); err != nil {
		t.Fatalf("SendBroadcast: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Type != domain.EventEscalation {
			t.Errorf("event type = %q, want %q", ev.Type, domain.EventEscalation)
		}
	default:
		t.Error("expected EventEscalation to be published")
	}

	msgs, _ := f.broker.GetUnread(ctx, agent.ID)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages to agent, got %d", len(msgs))
	}
}

func TestGetUnread_ReturnsOnlyUnread(t *testing.T) {
	f := setupBroker(t)
	ctx := context.Background()

	var firstID int64
	for i := 0; i < 3; i++ {
		msg := &domain.MailMessage{From: "s", To: "receiver", Type: "note", Payload: "x", Objective: "obj-1"}
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

	msg := &domain.MailMessage{From: "a", To: "b", Type: "note", Payload: "x", Objective: "obj-1"}
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
		msg := &domain.MailMessage{From: "a", To: "all-agent", Type: "note", Payload: "x", Objective: "obj-1"}
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

func TestIsBroadcast_CorrectlyIdentifiesAddresses(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"@all", true},
		{"@leads", true},
		{"@human", true},
		{"@stream:abc-123", true},
		{"@builders", true},
		{"agent-id-123", false},
		{"specific-name", false},
		{"", false},
	}
	for _, tc := range cases {
		got := IsBroadcast(tc.addr)
		if got != tc.want {
			t.Errorf("IsBroadcast(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
