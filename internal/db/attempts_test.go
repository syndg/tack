package db

import (
	"context"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestAttemptStoreListPreservesInsertionOrderForEqualTimestamps(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := NewAttemptStore(db.Conn())

	project := &domain.Project{ID: testProjectID}
	sameTime := time.Unix(1_700_000_000, 0)
	first := &domain.Attempt{ProjectID: project.ID, ObjectiveID: "obj-1", ExecutionID: "exec-1", StreamID: "stream-1", StepID: "build", AttemptNumber: 1, MaxAttempts: 3, FailureKind: domain.FailureQualityGate, Action: domain.RecoveryActionRerunPreviousAgent, Status: domain.AttemptStatusBlocked, CreatedAt: sameTime}
	second := &domain.Attempt{ProjectID: project.ID, ObjectiveID: "obj-1", ExecutionID: "exec-1", StreamID: "stream-1", StepID: "build", AttemptNumber: 2, MaxAttempts: 3, FailureKind: domain.FailureQualityGate, Action: domain.RecoveryActionAskHumanThenResume, Status: domain.AttemptStatusRunning, CreatedAt: sameTime}
	if err := store.Create(ctx, first); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if err := store.Create(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	attempts, err := store.ListByExecution(ctx, "exec-1")
	if err != nil {
		t.Fatalf("ListByExecution: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
	if attempts[0].ID != first.ID || attempts[1].ID != second.ID {
		t.Fatalf("attempt order = [%s %s], want [%s %s]", attempts[0].ID, attempts[1].ID, first.ID, second.ID)
	}
}
