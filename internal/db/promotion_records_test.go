package db

import (
	"context"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestPromotionRecordStorePersistsRecords(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "promote candidate")
	store := NewPromotionRecordStore(d.Conn())
	record := &domain.PromotionRecord{
		ProjectID:         testProjectID,
		ObjectiveID:       obj.ID,
		SourceCandidateID: "candidate-1",
		SourceInsightIDs:  []string{"insight-1", "insight-2"},
		Target:            domain.PromotionTargetProjectMemory,
		Status:            domain.PromotionStatusApproved,
		Confidence:        0.75,
		SupportCount:      2,
		Summary:           "Keep tests focused",
		Detail:            "Repeated review finding",
		Payload:           map[string]string{"kind_counts": "review_rejection:2"},
		CreatedAt:         time.Unix(10, 0),
		UpdatedAt:         time.Unix(11, 0),
	}
	if err := store.Create(ctx, record); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := store.GetByCandidateAndTarget(ctx, testProjectID, "candidate-1", domain.PromotionTargetProjectMemory)
	if err != nil {
		t.Fatalf("GetByCandidateAndTarget: %v", err)
	}
	if loaded.ProjectID != testProjectID || loaded.ObjectiveID != obj.ID || loaded.Status != domain.PromotionStatusApproved {
		t.Fatalf("loaded record = %+v", loaded)
	}
	if loaded.SupportCount != 2 || loaded.Confidence != 0.75 || len(loaded.SourceInsightIDs) != 2 || loaded.Payload["kind_counts"] != "review_rejection:2" {
		t.Fatalf("loaded metadata = %+v", loaded)
	}

	listed, err := store.ListByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != record.ID {
		t.Fatalf("listed = %+v", listed)
	}
}
