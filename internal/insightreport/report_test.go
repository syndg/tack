package insightreport

import (
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestBuildGroupsInsightsAndSummarizesCandidates(t *testing.T) {
	old := time.Unix(10, 0)
	recent := time.Unix(20, 0)
	report := Build("obj-1", []domain.ObjectiveInsight{
		{ID: "i-1", ObjectiveID: "obj-1", StreamID: "stream-1", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep tests focused", CreatedAt: old},
		{ID: "i-2", ObjectiveID: "obj-1", StreamID: "stream-1", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep tests focused", CreatedAt: recent},
		{ID: "i-3", ObjectiveID: "obj-1", Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance, Summary: "Use Bun", CreatedAt: time.Unix(15, 0)},
	}, []domain.CodificationCandidate{{ID: "candidate-1", EvidenceCount: 3}}, []domain.PromotionRecord{{ID: "promotion-1", SourceCandidateID: "candidate-1", Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusApproved, UpdatedAt: recent}, {ID: "promotion-2", SourceInsightIDs: []string{"i-3"}, Target: domain.PromotionTargetProjectMemory, Status: domain.PromotionStatusRejected, UpdatedAt: old}})

	if report.Summary.TotalInsights != 3 || report.Summary.TotalGroups != 2 || report.Summary.Candidates != 1 || report.Summary.Promotions.Approved != 1 || report.Summary.Promotions.Rejected != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Summary.PromotionTargets.ProjectMemory != 1 || report.Summary.PromotionTargets.Codification != 1 {
		t.Fatalf("promotion targets = %+v", report.Summary.PromotionTargets)
	}
	if report.Groups[0].Key != "review_rejection/reviewer/stream-1" || report.Groups[0].Count != 2 {
		t.Fatalf("first group = %+v", report.Groups[0])
	}
	if len(report.Groups[0].Summaries) != 1 || report.Groups[0].Summaries[0] != "Keep tests focused" {
		t.Fatalf("summaries = %#v", report.Groups[0].Summaries)
	}
	if report.Groups[0].FirstSeen != old || report.Groups[0].LastSeen != recent {
		t.Fatalf("time range = %s..%s", report.Groups[0].FirstSeen, report.Groups[0].LastSeen)
	}
	if len(report.CandidateMetadata) != 1 || report.CandidateMetadata[0].CandidateID != "candidate-1" {
		t.Fatalf("candidate metadata = %+v", report.CandidateMetadata)
	}
	metadata := report.CandidateMetadata[0]
	if metadata.SupportCount != 3 || metadata.Confidence != 1 || !metadata.ThresholdEligible || metadata.AutoApprovalEnabled || metadata.AutoApproved {
		t.Fatalf("threshold metadata = %+v", metadata)
	}
	if len(metadata.PromotionDecisions) != 1 || metadata.PromotionDecisions[0].Status != domain.PromotionStatusApproved || metadata.PromotionDecisions[0].Target != domain.PromotionTargetCodification {
		t.Fatalf("candidate promotion metadata = %+v", metadata.PromotionDecisions)
	}
	if len(report.Groups[1].PromotionDecisions) != 1 || report.Groups[1].PromotionDecisions[0].Status != domain.PromotionStatusRejected || report.Groups[1].PromotionDecisions[0].Target != domain.PromotionTargetProjectMemory {
		t.Fatalf("group promotion decisions = %+v", report.Groups[1].PromotionDecisions)
	}
}

func TestBuildHandlesEmptyInputs(t *testing.T) {
	report := Build("obj-empty", nil, nil)
	if report.ObjectiveID != "obj-empty" || report.Summary.TotalInsights != 0 || len(report.Groups) != 0 || len(report.Candidates) != 0 || len(report.CandidateMetadata) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestBuildMarksRejectedCandidateWithoutFreshProposal(t *testing.T) {
	candidate := domain.CodificationCandidate{ID: "candidate-rejected", EvidenceCount: 2}
	report := Build("obj-1", nil, []domain.CodificationCandidate{candidate}, []domain.PromotionRecord{{ID: "promotion-rejected", SourceCandidateID: candidate.ID, Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusRejected}})

	if report.Summary.Promotions.Proposed != 0 || report.Summary.Promotions.Rejected != 1 {
		t.Fatalf("promotion summary = %+v", report.Summary.Promotions)
	}
	if len(report.Promotions) != 1 || report.Promotions[0].Status != domain.PromotionStatusRejected {
		t.Fatalf("promotions = %+v", report.Promotions)
	}
	if len(report.CandidateMetadata) != 1 || !report.CandidateMetadata[0].PreviouslyRejected {
		t.Fatalf("candidate metadata = %+v", report.CandidateMetadata)
	}
}

func TestBuildCountsUndecidedCandidateAsProposal(t *testing.T) {
	report := Build("obj-1", nil, []domain.CodificationCandidate{{ID: "candidate-new", EvidenceCount: 2}})
	if report.Summary.Promotions.Proposed != 1 || report.Summary.Promotions.Approved != 0 || report.Summary.Promotions.Rejected != 0 {
		t.Fatalf("promotion summary = %+v", report.Summary.Promotions)
	}
}
