package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/syndg/deck/internal/domain"
)

func TestStoresIntegration(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()
	objectives := NewObjectiveStore(database.Conn())
	agents := NewAgentStore(database.Conn())
	mail := NewMailStore(database.Conn())
	events := NewEventStore(database.Conn())

	obj := &domain.Objective{Description: "integration objective"}
	if err := objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if obj.ID == "" {
		t.Fatal("expected objective ID to be set")
	}

	gotObj, err := objectives.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if gotObj.Description != obj.Description {
		t.Fatalf("unexpected objective description: %q", gotObj.Description)
	}

	if err := objectives.UpdateStatus(ctx, obj.ID, domain.ObjectiveStatusExecuting); err != nil {
		t.Fatalf("UpdateStatus objective: %v", err)
	}

	sessions := &domain.AgentSession{ObjectiveID: obj.ID, Role: domain.AgentRolePlanner}
	if err := agents.Create(ctx, sessions); err != nil {
		t.Fatalf("Create agent: %v", err)
	}
	gotAgent, err := agents.Get(ctx, sessions.ID)
	if err != nil {
		t.Fatalf("Get agent: %v", err)
	}
	if gotAgent.Role != domain.AgentRolePlanner {
		t.Fatalf("unexpected agent role: %q", gotAgent.Role)
	}

	msg := &domain.MailMessage{From: "planner", To: "worker", Type: "note", Payload: "hello", Objective: obj.ID}
	if err := mail.Send(ctx, msg); err != nil {
		t.Fatalf("Send mail: %v", err)
	}
	unread, err := mail.GetUnread(ctx, "worker")
	if err != nil {
		t.Fatalf("GetUnread: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected 1 unread message, got %d", len(unread))
	}
	if err := mail.MarkRead(ctx, msg.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	ev := &domain.Event{Type: domain.EventObjectiveCreated, Objective: obj.ID, Payload: "created"}
	if err := events.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert event: %v", err)
	}
	recent, err := events.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(recent) == 0 {
		t.Fatal("expected at least one event")
	}
}

func TestUpdateMissingReturnsNoRows(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	err = NewObjectiveStore(database.Conn()).UpdateStatus(context.Background(), "missing", domain.ObjectiveStatusFailed)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows-compatible error, got %v", err)
	}
}
