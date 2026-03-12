package db

import (
	"context"
	"testing"
	"time"

	"github.com/syndg/deck/internal/domain"
)

func TestMergeQueueStore_Enqueue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	entry := &domain.MergeEntry{
		StreamID:    "stream-1",
		PlanID:      "plan-1",
		ObjectiveID: "obj-1",
		Branch:      "deck/stream-1/builder-abc",
	}
	if err := store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if entry.ID == "" {
		t.Error("expected UUID to be generated")
	}
	if entry.Status != domain.MergeStatusPending {
		t.Errorf("status = %q, want %q", entry.Status, domain.MergeStatusPending)
	}
	if entry.CreatedAt == 0 {
		t.Error("expected created_at to be set")
	}
	if entry.UpdatedAt == 0 {
		t.Error("expected updated_at to be set")
	}

	// Verify round-trip via Get.
	got, err := store.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.StreamID != "stream-1" {
		t.Errorf("StreamID = %q, want %q", got.StreamID, "stream-1")
	}
	if got.PlanID != "plan-1" {
		t.Errorf("PlanID = %q, want %q", got.PlanID, "plan-1")
	}
	if got.ObjectiveID != "obj-1" {
		t.Errorf("ObjectiveID = %q, want %q", got.ObjectiveID, "obj-1")
	}
	if got.Branch != "deck/stream-1/builder-abc" {
		t.Errorf("Branch = %q, want %q", got.Branch, "deck/stream-1/builder-abc")
	}
}

func TestMergeQueueStore_Dequeue_FIFO(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	// Enqueue two entries with different created_at times.
	e1 := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	if err := store.Enqueue(ctx, e1); err != nil {
		t.Fatalf("Enqueue e1: %v", err)
	}

	// Ensure e2 has a later timestamp.
	time.Sleep(10 * time.Millisecond)
	e2 := &domain.MergeEntry{StreamID: "s2", Branch: "b2"}
	if err := store.Enqueue(ctx, e2); err != nil {
		t.Fatalf("Enqueue e2: %v", err)
	}

	got, err := store.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.ID != e1.ID {
		t.Errorf("Dequeue returned ID %q, want %q (FIFO order)", got.ID, e1.ID)
	}
}

func TestMergeQueueStore_Dequeue_EmptyQueue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	got, err := store.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty queue, got %+v", got)
	}
}

func TestMergeQueueStore_Dequeue_SkipsNonPending(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	// Enqueue and mark first entry as "merging".
	e1 := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	if err := store.Enqueue(ctx, e1); err != nil {
		t.Fatalf("Enqueue e1: %v", err)
	}
	if err := store.UpdateStatus(ctx, e1.ID, domain.MergeStatusMerging, 0, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// Enqueue second entry (still pending).
	time.Sleep(10 * time.Millisecond)
	e2 := &domain.MergeEntry{StreamID: "s2", Branch: "b2"}
	if err := store.Enqueue(ctx, e2); err != nil {
		t.Fatalf("Enqueue e2: %v", err)
	}

	got, err := store.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.ID != e2.ID {
		t.Errorf("Dequeue returned ID %q, want %q (should skip non-pending)", got.ID, e2.ID)
	}
}

func TestMergeQueueStore_GetByStream(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	// Enqueue two entries for the same stream — GetByStream should return the most recent.
	e1 := &domain.MergeEntry{StreamID: "stream-x", Branch: "b1"}
	if err := store.Enqueue(ctx, e1); err != nil {
		t.Fatalf("Enqueue e1: %v", err)
	}
	// Push e1's created_at into the past so e2 is definitively newer.
	d.Conn().ExecContext(ctx, `UPDATE merge_queue SET created_at = created_at - 10 WHERE id = ?`, e1.ID)

	e2 := &domain.MergeEntry{StreamID: "stream-x", Branch: "b2"}
	if err := store.Enqueue(ctx, e2); err != nil {
		t.Fatalf("Enqueue e2: %v", err)
	}

	got, err := store.GetByStream(ctx, "stream-x")
	if err != nil {
		t.Fatalf("GetByStream: %v", err)
	}
	if got.ID != e2.ID {
		t.Errorf("GetByStream returned ID %q, want %q (most recent)", got.ID, e2.ID)
	}
}

func TestMergeQueueStore_UpdateStatus(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	entry := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	if err := store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Push entry's timestamps into the past so UpdateStatus creates a newer updated_at.
	d.Conn().ExecContext(ctx, `UPDATE merge_queue SET created_at = created_at - 10, updated_at = updated_at - 10 WHERE id = ?`, entry.ID)
	past, _ := store.Get(ctx, entry.ID)
	beforeUpdate := past.UpdatedAt

	if err := store.UpdateStatus(ctx, entry.ID, domain.MergeStatusMerged, 1, "", `{"files_changed":3}`); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := store.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.MergeStatusMerged {
		t.Errorf("Status = %q, want %q", got.Status, domain.MergeStatusMerged)
	}
	if got.Tier != 1 {
		t.Errorf("Tier = %d, want 1", got.Tier)
	}
	if got.DiffStat != `{"files_changed":3}` {
		t.Errorf("DiffStat = %q, want %q", got.DiffStat, `{"files_changed":3}`)
	}
	if got.UpdatedAt <= beforeUpdate {
		t.Errorf("expected updated_at (%d) > beforeUpdate (%d)", got.UpdatedAt, beforeUpdate)
	}
}

func TestMergeQueueStore_UpdateStatus_WithError(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	entry := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	store.Enqueue(ctx, entry)

	if err := store.UpdateStatus(ctx, entry.ID, domain.MergeStatusFailed, 2, "merge conflicts detected", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, _ := store.Get(ctx, entry.ID)
	if got.Error != "merge conflicts detected" {
		t.Errorf("Error = %q, want %q", got.Error, "merge conflicts detected")
	}
	if got.Tier != 2 {
		t.Errorf("Tier = %d, want 2", got.Tier)
	}
}

func TestMergeQueueStore_ListByObjective(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	// Entries for objective A.
	for i := 0; i < 3; i++ {
		store.Enqueue(ctx, &domain.MergeEntry{
			StreamID:    "sA",
			ObjectiveID: "obj-a",
			Branch:      "bA",
		})
	}
	// Entry for objective B.
	store.Enqueue(ctx, &domain.MergeEntry{
		StreamID:    "sB",
		ObjectiveID: "obj-b",
		Branch:      "bB",
	})

	entries, err := store.ListByObjective(ctx, "obj-a")
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("len = %d, want 3", len(entries))
	}
	for _, e := range entries {
		if e.ObjectiveID != "obj-a" {
			t.Errorf("ObjectiveID = %q, want %q", e.ObjectiveID, "obj-a")
		}
	}
}

func TestMergeQueueStore_ListAll(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	e1 := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	store.Enqueue(ctx, e1)
	time.Sleep(10 * time.Millisecond)

	e2 := &domain.MergeEntry{StreamID: "s2", Branch: "b2"}
	store.Enqueue(ctx, e2)
	store.UpdateStatus(ctx, e2.ID, domain.MergeStatusMerged, 1, "", "")

	entries, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	if entries[0].ID != e1.ID || entries[1].ID != e2.ID {
		t.Fatalf("ListAll order = [%s, %s], want [%s, %s]", entries[0].ID, entries[1].ID, e1.ID, e2.ID)
	}
	if entries[1].Status != domain.MergeStatusMerged {
		t.Errorf("entries[1].Status = %q, want %q", entries[1].Status, domain.MergeStatusMerged)
	}
}

func TestMergeQueueStore_ListPending(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	e1 := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	store.Enqueue(ctx, e1)

	time.Sleep(10 * time.Millisecond)
	e2 := &domain.MergeEntry{StreamID: "s2", Branch: "b2"}
	store.Enqueue(ctx, e2)

	// Mark e1 as merged — should not appear in ListPending.
	store.UpdateStatus(ctx, e1.ID, domain.MergeStatusMerged, 1, "", "")

	pending, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len = %d, want 1", len(pending))
	}
	if pending[0].ID != e2.ID {
		t.Errorf("pending[0].ID = %q, want %q", pending[0].ID, e2.ID)
	}
}

func TestMergeQueueStore_ListPending_OrderedByCreatedAt(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	e1 := &domain.MergeEntry{StreamID: "s1", Branch: "b1"}
	store.Enqueue(ctx, e1)
	time.Sleep(10 * time.Millisecond)

	e2 := &domain.MergeEntry{StreamID: "s2", Branch: "b2"}
	store.Enqueue(ctx, e2)

	pending, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("len = %d, want 2", len(pending))
	}
	// First entry should be the oldest.
	if pending[0].ID != e1.ID {
		t.Errorf("first pending ID = %q, want %q (oldest first)", pending[0].ID, e1.ID)
	}
	if pending[1].ID != e2.ID {
		t.Errorf("second pending ID = %q, want %q", pending[1].ID, e2.ID)
	}
}

func TestMergeQueueStore_CountPending(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	store := NewMergeQueueStore(d.Conn())

	// Empty queue.
	count, err := store.CountPending(ctx)
	if err != nil {
		t.Fatalf("CountPending: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// Add two pending entries.
	store.Enqueue(ctx, &domain.MergeEntry{StreamID: "s1", Branch: "b1"})
	store.Enqueue(ctx, &domain.MergeEntry{StreamID: "s2", Branch: "b2"})

	count, err = store.CountPending(ctx)
	if err != nil {
		t.Fatalf("CountPending: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	// Mark one as merged — count should decrease.
	entries, _ := store.ListPending(ctx)
	store.UpdateStatus(ctx, entries[0].ID, domain.MergeStatusMerged, 1, "", "")

	count, err = store.CountPending(ctx)
	if err != nil {
		t.Fatalf("CountPending after update: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
