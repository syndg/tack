package db

import (
	"context"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestObjectiveInsightStore_Create_RefreshesCodificationCandidates(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	obj := createTestObjective(t, NewObjectiveStore(d.Conn()), "test objective")
	insightStore := NewObjectiveInsightStore(d.Conn())
	candidateStore := NewCodificationCandidateStore(d.Conn())
	insightStore.BindCodificationStore(candidateStore)

	first := &domain.ObjectiveInsight{ObjectiveID: obj.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit"}
	if err := insightStore.Create(ctx, first); err != nil {
		t.Fatalf("Create first insight: %v", err)
	}
	candidates, err := candidateStore.ListByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("ListByObjective after first insight: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidate count after first insight = %d, want 0", len(candidates))
	}
	second := &domain.ObjectiveInsight{ObjectiveID: obj.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit"}
	if err := insightStore.Create(ctx, second); err != nil {
		t.Fatalf("Create second insight: %v", err)
	}
	candidates, err = candidateStore.ListByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("ListByObjective after second insight: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count after second insight = %d, want 1", len(candidates))
	}
	if candidates[0].Target != domain.CodificationTargetReviewCheck {
		t.Fatalf("candidate target = %q", candidates[0].Target)
	}
	if candidates[0].EvidenceCount != 2 {
		t.Fatalf("candidate evidence_count = %d", candidates[0].EvidenceCount)
	}
}
