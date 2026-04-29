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
		Candidates: []domain.CodificationCandidate{{ID: "candidate-1", Target: domain.CodificationTargetReviewCheck, Status: domain.CodificationStatusProposed, EvidenceCount: 2, Instruction: "Preserve review coverage"}},
	}
	var out bytes.Buffer
	formatInsightReport(&out, domain.Objective{Description: "Add operational memory review"}, report)
	got := out.String()
	for _, want := range []string{"Insights:   2 across 1 groups", "review_rejection/reviewer", "Keep review checks explicit", "candidate-1", "review_check, proposed, evidence=2"} {
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
