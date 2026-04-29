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
	}, []domain.CodificationCandidate{{ID: "candidate-1", EvidenceCount: 3}})

	if report.Summary.TotalInsights != 3 || report.Summary.TotalGroups != 2 || report.Summary.Candidates != 1 {
		t.Fatalf("summary = %+v", report.Summary)
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
}

func TestBuildHandlesEmptyInputs(t *testing.T) {
	report := Build("obj-empty", nil, nil)
	if report.ObjectiveID != "obj-empty" || report.Summary.TotalInsights != 0 || len(report.Groups) != 0 || len(report.Candidates) != 0 || len(report.CandidateMetadata) != 0 {
		t.Fatalf("report = %+v", report)
	}
}
