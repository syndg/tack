package insightreport

import (
	"sort"
	"strings"
	"time"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/promotions"
)

type Summary struct {
	TotalInsights    int              `json:"total_insights"`
	TotalGroups      int              `json:"total_groups"`
	Candidates       int              `json:"candidates"`
	Promotions       PromotionSummary `json:"promotions"`
	PromotionTargets TargetSummary    `json:"promotion_targets"`
}

type PromotionSummary struct {
	Proposed int `json:"proposed"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
}

type TargetSummary struct {
	ProjectMemory int `json:"project_memory"`
	Codification  int `json:"codification"`
}

type Group struct {
	Key                string                        `json:"key"`
	Kind               domain.ObjectiveInsightKind   `json:"kind"`
	Source             domain.ObjectiveInsightSource `json:"source"`
	StreamID           string                        `json:"stream_id,omitempty"`
	Count              int                           `json:"count"`
	FirstSeen          time.Time                     `json:"first_seen"`
	LastSeen           time.Time                     `json:"last_seen"`
	Summaries          []string                      `json:"summaries"`
	Insights           []domain.ObjectiveInsight     `json:"insights"`
	PromotionDecisions []PromotionDecision           `json:"promotion_decisions,omitempty"`
}

type Report struct {
	ObjectiveID       string                         `json:"objective_id"`
	Summary           Summary                        `json:"summary"`
	Groups            []Group                        `json:"groups"`
	Candidates        []domain.CodificationCandidate `json:"candidates"`
	CandidateMetadata []CandidateMetadata            `json:"candidate_metadata"`
	Promotions        []domain.PromotionRecord       `json:"promotions"`
}

type PromotionDecision struct {
	RecordID  string                 `json:"record_id"`
	SourceID  string                 `json:"source_id"`
	Target    domain.PromotionTarget `json:"target"`
	Status    domain.PromotionStatus `json:"status"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type CandidateMetadata struct {
	CandidateID         string              `json:"candidate_id"`
	SupportCount        int                 `json:"support_count"`
	Confidence          float64             `json:"confidence"`
	ThresholdEligible   bool                `json:"threshold_eligible"`
	AutoApprovalEnabled bool                `json:"auto_approval_enabled"`
	AutoApproved        bool                `json:"auto_approved"`
	PromotionDecisions  []PromotionDecision `json:"promotion_decisions,omitempty"`
	PreviouslyRejected  bool                `json:"previously_rejected,omitempty"`
}

type DetailKind string

const (
	DetailKindInsight   DetailKind = "insight"
	DetailKindCandidate DetailKind = "candidate"
)

type Detail struct {
	Kind               DetailKind                    `json:"kind"`
	Insight            *domain.ObjectiveInsight      `json:"insight,omitempty"`
	Candidate          *domain.CodificationCandidate `json:"candidate,omitempty"`
	PromotionDecisions []PromotionDecision           `json:"promotion_decisions,omitempty"`
}

func Build(objectiveID string, insights []domain.ObjectiveInsight, candidates []domain.CodificationCandidate, promotionRecords ...[]domain.PromotionRecord) Report {
	promotions := flattenPromotions(promotionRecords)
	groups := groupInsights(insights, promotions)
	return Report{
		ObjectiveID: objectiveID,
		Summary: Summary{
			TotalInsights:    len(insights),
			TotalGroups:      len(groups),
			Candidates:       len(candidates),
			Promotions:       summarizePromotionStatuses(promotions, candidates),
			PromotionTargets: summarizePromotionTargets(promotions),
		},
		Groups:            groups,
		Candidates:        append([]domain.CodificationCandidate(nil), candidates...),
		CandidateMetadata: candidateMetadata(candidates, promotions),
		Promotions:        append([]domain.PromotionRecord(nil), promotions...),
	}
}

func candidateMetadata(candidates []domain.CodificationCandidate, promotionRecords []domain.PromotionRecord) []CandidateMetadata {
	metadata := make([]CandidateMetadata, 0, len(candidates))
	for _, candidate := range candidates {
		threshold := promotions.EvaluateThreshold(candidate.EvidenceCount, promotions.DefaultThresholdConfig())
		decisions := DecisionsForCandidate(promotionRecords, candidate.ID)
		metadata = append(metadata, CandidateMetadata{
			CandidateID:         candidate.ID,
			SupportCount:        threshold.SupportCount,
			Confidence:          threshold.Confidence,
			ThresholdEligible:   threshold.ThresholdEligible,
			AutoApprovalEnabled: threshold.AutoApprovalEnabled,
			AutoApproved:        threshold.AutoApproved,
			PromotionDecisions:  decisions,
			PreviouslyRejected:  hasRejectedDecision(decisions),
		})
	}
	return metadata
}

func groupInsights(insights []domain.ObjectiveInsight, promotions []domain.PromotionRecord) []Group {
	byKey := map[string]*Group{}
	for _, insight := range insights {
		key := groupKey(insight)
		group := byKey[key]
		if group == nil {
			group = &Group{Key: key, Kind: insight.Kind, Source: insight.Source, StreamID: insight.StreamID, FirstSeen: insight.CreatedAt, LastSeen: insight.CreatedAt}
			byKey[key] = group
		}
		group.Count++
		group.Insights = append(group.Insights, insight)
		if insight.CreatedAt.Before(group.FirstSeen) || group.FirstSeen.IsZero() {
			group.FirstSeen = insight.CreatedAt
		}
		if insight.CreatedAt.After(group.LastSeen) {
			group.LastSeen = insight.CreatedAt
		}
		addSummary(group, insight.Summary)
		group.PromotionDecisions = append(group.PromotionDecisions, DecisionsForInsight(promotions, insight.ID)...)
	}

	groups := make([]Group, 0, len(byKey))
	for _, group := range byKey {
		sort.Slice(group.Insights, func(i, j int) bool {
			return group.Insights[i].CreatedAt.After(group.Insights[j].CreatedAt)
		})
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if !groups[i].LastSeen.Equal(groups[j].LastSeen) {
			return groups[i].LastSeen.After(groups[j].LastSeen)
		}
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

func flattenPromotions(records [][]domain.PromotionRecord) []domain.PromotionRecord {
	if len(records) == 0 {
		return nil
	}
	return append([]domain.PromotionRecord(nil), records[0]...)
}

func summarizePromotionStatuses(promotions []domain.PromotionRecord, candidates []domain.CodificationCandidate) PromotionSummary {
	var summary PromotionSummary
	reviewedCandidates := map[string]bool{}
	for _, record := range promotions {
		switch record.Status {
		case domain.PromotionStatusApproved:
			summary.Approved++
		case domain.PromotionStatusRejected:
			summary.Rejected++
		case domain.PromotionStatusProposed:
			summary.Proposed++
		}
		if record.SourceCandidateID != "" {
			reviewedCandidates[record.SourceCandidateID] = true
		}
	}
	for _, candidate := range candidates {
		if !reviewedCandidates[candidate.ID] {
			summary.Proposed++
		}
	}
	return summary
}

func summarizePromotionTargets(promotions []domain.PromotionRecord) TargetSummary {
	var summary TargetSummary
	for _, record := range promotions {
		switch record.Target {
		case domain.PromotionTargetProjectMemory:
			summary.ProjectMemory++
		case domain.PromotionTargetCodification:
			summary.Codification++
		}
	}
	return summary
}

func DecisionsForCandidate(promotions []domain.PromotionRecord, candidateID string) []PromotionDecision {
	var decisions []PromotionDecision
	for _, record := range promotions {
		if record.SourceCandidateID == candidateID {
			decisions = append(decisions, promotionDecision(record, candidateID))
		}
	}
	return sortedDecisions(decisions)
}

func DecisionsForInsight(promotions []domain.PromotionRecord, insightID string) []PromotionDecision {
	var decisions []PromotionDecision
	for _, record := range promotions {
		for _, sourceInsightID := range record.SourceInsightIDs {
			if sourceInsightID == insightID {
				decisions = append(decisions, promotionDecision(record, insightID))
			}
		}
	}
	return sortedDecisions(decisions)
}

func promotionDecision(record domain.PromotionRecord, sourceID string) PromotionDecision {
	return PromotionDecision{RecordID: record.ID, SourceID: sourceID, Target: record.Target, Status: record.Status, UpdatedAt: record.UpdatedAt}
}

func sortedDecisions(decisions []PromotionDecision) []PromotionDecision {
	sort.Slice(decisions, func(i, j int) bool {
		if !decisions[i].UpdatedAt.Equal(decisions[j].UpdatedAt) {
			return decisions[i].UpdatedAt.After(decisions[j].UpdatedAt)
		}
		return decisions[i].RecordID < decisions[j].RecordID
	})
	return decisions
}

func hasRejectedDecision(decisions []PromotionDecision) bool {
	for _, decision := range decisions {
		if decision.Status == domain.PromotionStatusRejected {
			return true
		}
	}
	return false
}

func groupKey(insight domain.ObjectiveInsight) string {
	parts := []string{string(insight.Kind), string(insight.Source)}
	if insight.StreamID != "" {
		parts = append(parts, insight.StreamID)
	}
	return strings.Join(parts, "/")
}

func addSummary(group *Group, summary string) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	for _, existing := range group.Summaries {
		if existing == summary {
			return
		}
	}
	group.Summaries = append(group.Summaries, summary)
}
