package codification

import (
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestDeriveCandidates_PromotesRepeatedSignals(t *testing.T) {
	now := time.Unix(100, 0)
	candidates := DeriveCandidates("proj-1", "obj-1", []domain.ObjectiveInsight{
		{ObjectiveID: "obj-1", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit", CreatedAt: now},
		{ObjectiveID: "obj-1", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit", CreatedAt: now.Add(time.Minute)},
		{ObjectiveID: "obj-1", Source: domain.InsightSourcePlanner, Kind: domain.InsightKindPlanQualityGateEdit, Summary: "Add focused auth regression gate", CreatedAt: now.Add(2 * time.Minute)},
		{ObjectiveID: "obj-1", Source: domain.InsightSourcePlanner, Kind: domain.InsightKindPlanQualityGateEdit, Summary: "Add focused auth regression gate", CreatedAt: now.Add(3 * time.Minute)},
	})
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2: %#v", len(candidates), candidates)
	}
	if candidates[0].EvidenceCount < candidates[1].EvidenceCount {
		t.Fatalf("candidates not sorted by evidence count: %#v", candidates)
	}
	seenTargets := map[domain.CodificationCandidateTarget]bool{}
	for _, candidate := range candidates {
		seenTargets[candidate.Target] = true
		if candidate.Status != domain.CodificationStatusProposed {
			t.Fatalf("candidate status = %q", candidate.Status)
		}
		if candidate.EvidenceCount != 2 {
			t.Fatalf("candidate evidence count = %d, want 2", candidate.EvidenceCount)
		}
	}
	if !seenTargets[domain.CodificationTargetReviewCheck] || !seenTargets[domain.CodificationTargetQualityGate] {
		t.Fatalf("targets = %#v", seenTargets)
	}
}

func TestDeriveCandidates_IgnoresSingletonSignals(t *testing.T) {
	candidates := DeriveCandidates("proj-1", "obj-1", []domain.ObjectiveInsight{{ObjectiveID: "obj-1", Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance, Summary: "Keep API stable"}})
	if len(candidates) != 0 {
		t.Fatalf("candidate count = %d, want 0: %#v", len(candidates), candidates)
	}
}
