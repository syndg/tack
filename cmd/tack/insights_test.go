package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
)

func TestFormatInsightReportShowsGroupsAndCandidates(t *testing.T) {
	report := insightreport.Report{
		ObjectiveID: "obj-1",
		Summary:     insightreport.Summary{TotalInsights: 2, TotalGroups: 1, Candidates: 1},
		Groups: []insightreport.Group{{
			Kind:      domain.InsightKindReviewRejection,
			Source:    domain.InsightSourceReviewer,
			StreamID:  "stream-123456789",
			Count:     2,
			FirstSeen: time.Unix(10, 0),
			LastSeen:  time.Unix(20, 0),
			Summaries: []string{"Keep review checks explicit"},
		}},
		Candidates:        []domain.CodificationCandidate{{ID: "candidate-1", Target: domain.CodificationTargetReviewCheck, Status: domain.CodificationStatusProposed, EvidenceCount: 3, Instruction: "Preserve review coverage"}},
		CandidateMetadata: []insightreport.CandidateMetadata{{CandidateID: "candidate-1", SupportCount: 3, Confidence: 1, ThresholdEligible: true}},
	}
	var out bytes.Buffer
	formatInsightReport(&out, domain.Objective{Description: "Add operational memory review"}, report)
	got := out.String()
	for _, want := range []string{"Insights:   2 across 1 groups", "review_rejection/reviewer", "Keep review checks explicit", "candidate-1", "review_check, proposed, evidence=3, confidence=1.00, threshold=eligible, auto-approval=off"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatInsightDetailShowsRawInsight(t *testing.T) {
	detail := insightreport.Detail{Kind: insightreport.DetailKindInsight, Insight: &domain.ObjectiveInsight{
		ID: "insight-1", ObjectiveID: "obj-1", StreamID: "stream-1", PlanID: "plan-1", ExecutionID: "execution-1",
		Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Add coverage", Detail: "Reviewer asked for coverage", Payload: map[string]string{"step_id": "review"}, CreatedAt: time.Unix(10, 0),
	}}
	var out bytes.Buffer
	formatInsightDetail(&out, detail)
	got := out.String()
	for _, want := range []string{"Insight:   insight-1", "Objective: obj-1", "Stream:    stream-1", "Plan:      plan-1", "Execution: execution-1", "Source:    reviewer", "Kind:      review_rejection", "Summary:   Add coverage", "Detail:    Reviewer asked for coverage", "step_id: review"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatInsightDetailShowsCandidate(t *testing.T) {
	detail := insightreport.Detail{Kind: insightreport.DetailKindCandidate, Candidate: &domain.CodificationCandidate{
		ID: "candidate-1", ObjectiveID: "obj-1", Target: domain.CodificationTargetReviewCheck, Status: domain.CodificationStatusProposed, EvidenceCount: 2, Title: "Proposed review check", Instruction: "Keep auth coverage", Rationale: "Repeated reviewer finding", Payload: map[string]string{"kind_counts": "review_rejection:2"}, CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(20, 0),
	}}
	var out bytes.Buffer
	formatInsightDetail(&out, detail)
	got := out.String()
	for _, want := range []string{"Candidate: candidate-1", "Objective: obj-1", "Target:    review_check", "Status:    proposed", "Evidence:  2", "Instruction:", "Keep auth coverage", "Rationale:", "Repeated reviewer finding", "kind_counts: review_rejection:2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatPromotionRecordShowsDecision(t *testing.T) {
	record := domain.PromotionRecord{ID: "promotion-1", SourceCandidateID: "candidate-1", Target: domain.PromotionTargetProjectMemory, Status: domain.PromotionStatusApproved, SupportCount: 2, Confidence: 0.67, Summary: "Preserve review coverage"}
	var out bytes.Buffer
	formatPromotionRecord(&out, "Approved", record)
	got := out.String()
	for _, want := range []string{"Approved promotion: promotion-1", "Source: candidate-1", "Target: project-memory", "Status: approved", "Support: 2 confidence=0.67", "Preserve review coverage"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatPromotionRecordShowsRawInsightSource(t *testing.T) {
	record := domain.PromotionRecord{ID: "promotion-1", SourceInsightIDs: []string{"insight-1"}, Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusRejected, SupportCount: 1, Confidence: 0.33, Summary: "Noisy learning"}
	var out bytes.Buffer
	formatPromotionRecord(&out, "Rejected", record)
	got := out.String()
	for _, want := range []string{"Rejected promotion: promotion-1", "Source: insight-1", "Target: codification", "Status: rejected", "Noisy learning"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
