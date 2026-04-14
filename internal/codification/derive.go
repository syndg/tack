package codification

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/syndg/tack/internal/contractpatch"
	"github.com/syndg/tack/internal/domain"
)

type aggregate struct {
	Target        domain.CodificationCandidateTarget
	Instruction   string
	Rationale     string
	EvidenceCount int
	Kinds         map[domain.ObjectiveInsightKind]int
	Sources       map[domain.ObjectiveInsightSource]int
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
}

func DeriveCandidates(projectID, objectiveID string, insights []domain.ObjectiveInsight) []domain.CodificationCandidate {
	agg := map[string]*aggregate{}
	for _, insight := range insights {
		patches := contractpatch.Compile([]domain.ObjectiveInsight{insight}, insight.StreamID)
		if len(patches) == 0 {
			continue
		}
		patch := patches[0]
		target := classifyTarget(insight.Kind)
		key := strings.ToLower(strings.TrimSpace(string(target) + "\n" + patch.Instruction))
		entry := agg[key]
		if entry == nil {
			entry = &aggregate{
				Target:      target,
				Instruction: patch.Instruction,
				Rationale:   patch.Rationale,
				Kinds:       map[domain.ObjectiveInsightKind]int{},
				Sources:     map[domain.ObjectiveInsightSource]int{},
				FirstSeenAt: insight.CreatedAt,
				LastSeenAt:  insight.CreatedAt,
			}
			agg[key] = entry
		}
		entry.EvidenceCount++
		entry.Kinds[insight.Kind]++
		entry.Sources[insight.Source]++
		if entry.Rationale == "" && patch.Rationale != "" {
			entry.Rationale = patch.Rationale
		}
		if insight.CreatedAt.Before(entry.FirstSeenAt) || entry.FirstSeenAt.IsZero() {
			entry.FirstSeenAt = insight.CreatedAt
		}
		if insight.CreatedAt.After(entry.LastSeenAt) {
			entry.LastSeenAt = insight.CreatedAt
		}
	}
	var out []domain.CodificationCandidate
	for _, entry := range agg {
		if entry.EvidenceCount < 2 {
			continue
		}
		out = append(out, domain.CodificationCandidate{
			ID:            candidateID(objectiveID, entry.Target, entry.Instruction),
			ProjectID:     projectID,
			ObjectiveID:   objectiveID,
			Status:        domain.CodificationStatusProposed,
			Target:        entry.Target,
			Title:         titleFor(entry.Target, entry.Instruction),
			Instruction:   entry.Instruction,
			Rationale:     entry.Rationale,
			EvidenceCount: entry.EvidenceCount,
			Payload: map[string]string{
				"kind_counts":   summarizeKinds(entry.Kinds),
				"source_counts": summarizeSources(entry.Sources),
			},
			CreatedAt: entry.FirstSeenAt,
			UpdatedAt: entry.LastSeenAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EvidenceCount != out[j].EvidenceCount {
			return out[i].EvidenceCount > out[j].EvidenceCount
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Instruction < out[j].Instruction
	})
	return out
}

func AutoAppliedPatches(projectID, objectiveID string, insights []domain.ObjectiveInsight) []domain.StreamCardPatch {
	candidates := DeriveCandidates(projectID, objectiveID, insights)
	patches := make([]domain.StreamCardPatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Target == domain.CodificationTargetQualityGate {
			continue
		}
		patches = append(patches, domain.StreamCardPatch{
			Instruction:   candidate.Instruction,
			Rationale:     candidate.Rationale,
			CandidateID:   candidate.ID,
			EvidenceCount: candidate.EvidenceCount,
		})
	}
	return patches
}

func classifyTarget(kind domain.ObjectiveInsightKind) domain.CodificationCandidateTarget {
	switch kind {
	case domain.InsightKindPlanQualityGateEdit:
		return domain.CodificationTargetQualityGate
	case domain.InsightKindReviewRejection, domain.InsightKindContractGap, domain.InsightKindContractBlocked:
		return domain.CodificationTargetReviewCheck
	default:
		return domain.CodificationTargetRule
	}
}

func candidateID(objectiveID string, target domain.CodificationCandidateTarget, instruction string) string {
	sum := sha1.Sum([]byte(objectiveID + "\n" + string(target) + "\n" + instruction))
	return "candidate-" + hex.EncodeToString(sum[:8])
}

func titleFor(target domain.CodificationCandidateTarget, instruction string) string {
	prefix := "Proposed rule"
	switch target {
	case domain.CodificationTargetReviewCheck:
		prefix = "Proposed review check"
	case domain.CodificationTargetQualityGate:
		prefix = "Proposed quality gate"
	}
	return fmt.Sprintf("%s: %s", prefix, instruction)
}

func summarizeKinds(counts map[domain.ObjectiveInsightKind]int) string {
	parts := make([]string, 0, len(counts))
	for kind, count := range counts {
		parts = append(parts, fmt.Sprintf("%s:%d", kind, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func summarizeSources(counts map[domain.ObjectiveInsightSource]int) string {
	parts := make([]string, 0, len(counts))
	for source, count := range counts {
		parts = append(parts, fmt.Sprintf("%s:%d", source, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
