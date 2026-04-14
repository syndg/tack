package contractpatch

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
)

func Compile(insights []domain.ObjectiveInsight, streamID string) []domain.StreamCardPatch {
	patches := make([]domain.StreamCardPatch, 0, len(insights))
	seen := map[string]struct{}{}
	for _, insight := range insights {
		if streamID != "" && insight.StreamID != "" && insight.StreamID != streamID {
			continue
		}
		patch, ok := insightToPatch(insight, streamID)
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(patch.Instruction + "\n" + patch.Rationale + "\n" + patch.ScopeNote))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		patches = append(patches, patch)
	}
	return patches
}

func Merge(groups ...[]domain.StreamCardPatch) []domain.StreamCardPatch {
	var merged []domain.StreamCardPatch
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, patch := range group {
			key := strings.ToLower(strings.TrimSpace(patch.Instruction + "\n" + patch.Rationale + "\n" + patch.ScopeNote + "\n" + patch.CandidateID))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, patch)
		}
	}
	return merged
}

func insightToPatch(insight domain.ObjectiveInsight, streamID string) (domain.StreamCardPatch, bool) {
	text := strings.TrimSpace(insight.Summary)
	rationale := strings.TrimSpace(insight.Detail)
	if text == "" {
		text = rationale
		rationale = ""
	}
	if text == "" {
		return domain.StreamCardPatch{}, false
	}
	patch := domain.StreamCardPatch{
		Source:    insight.Source,
		Kind:      insight.Kind,
		Rationale: rationale,
	}
	if insight.StreamID != "" && insight.StreamID != streamID {
		patch.ScopeNote = fmt.Sprintf("stream %s", insight.StreamID)
	}
	switch insight.Kind {
	case domain.InsightKindReviewRejection:
		patch.Instruction = "Preserve this previously rejected requirement: " + text
	case domain.InsightKindContractGap:
		patch.Instruction = "Add this previously missing dossier-backed requirement to the contract: " + text
	case domain.InsightKindContractBlocked:
		patch.Instruction = "Repair or clarify this previously blocked contract requirement before proceeding: " + text
	case domain.InsightKindRetryGuidance:
		patch.Instruction = "Honor this explicit human retry guidance: " + text
	case domain.InsightKindDossierEdit:
		patch.Instruction = "Carry forward this dossier correction: " + text
	case domain.InsightKindPlanApproval:
		patch.Instruction = "Carry forward this approved planning rationale: " + text
	case domain.InsightKindPlanQualityGateEdit:
		patch.Instruction = "Carry forward this quality-gate adjustment: " + text
	default:
		return domain.StreamCardPatch{}, false
	}
	return patch, true
}
