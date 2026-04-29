package db

import (
	"context"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestObjectiveInsightStore_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	objective := createTestObjective(t, NewObjectiveStore(d.Conn()), "capture insights")
	store := NewObjectiveInsightStore(d.Conn())

	insight := &domain.ObjectiveInsight{
		ObjectiveID: objective.ID,
		Source:      domain.InsightSourceReviewer,
		Kind:        domain.InsightKindReviewRejection,
		Summary:     "Reviewer requested regression coverage",
		Detail:      "Add coverage for the error path before merge.",
		Payload:     map[string]string{"step_id": "review", "stream_card_field": "proof_scope"},
	}
	if err := store.Create(ctx, insight); err != nil {
		t.Fatalf("Create: %v", err)
	}
	insights, err := store.ListByObjective(ctx, objective.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("insight count = %d, want 1", len(insights))
	}
	if insights[0].ProjectID != testProjectID {
		t.Fatalf("project_id = %q, want %q", insights[0].ProjectID, testProjectID)
	}
	if insights[0].Kind != domain.InsightKindReviewRejection {
		t.Fatalf("kind = %q", insights[0].Kind)
	}
	if insights[0].Payload["stream_card_field"] != "proof_scope" {
		t.Fatalf("payload = %#v", insights[0].Payload)
	}
	got, err := store.Get(ctx, insight.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != insight.ID || got.Detail != insight.Detail || got.Payload["step_id"] != "review" {
		t.Fatalf("got insight = %+v", got)
	}
}
