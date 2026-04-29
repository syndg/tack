package insightreport

import (
	"sort"
	"strings"
	"time"

	"github.com/syndg/tack/internal/domain"
)

type Summary struct {
	TotalInsights int `json:"total_insights"`
	TotalGroups   int `json:"total_groups"`
	Candidates    int `json:"candidates"`
}

type Group struct {
	Key       string                        `json:"key"`
	Kind      domain.ObjectiveInsightKind   `json:"kind"`
	Source    domain.ObjectiveInsightSource `json:"source"`
	StreamID  string                        `json:"stream_id,omitempty"`
	Count     int                           `json:"count"`
	FirstSeen time.Time                     `json:"first_seen"`
	LastSeen  time.Time                     `json:"last_seen"`
	Summaries []string                      `json:"summaries"`
	Insights  []domain.ObjectiveInsight     `json:"insights"`
}

type Report struct {
	ObjectiveID string                         `json:"objective_id"`
	Summary     Summary                        `json:"summary"`
	Groups      []Group                        `json:"groups"`
	Candidates  []domain.CodificationCandidate `json:"candidates"`
}

func Build(objectiveID string, insights []domain.ObjectiveInsight, candidates []domain.CodificationCandidate) Report {
	groups := groupInsights(insights)
	return Report{
		ObjectiveID: objectiveID,
		Summary: Summary{
			TotalInsights: len(insights),
			TotalGroups:   len(groups),
			Candidates:    len(candidates),
		},
		Groups:     groups,
		Candidates: append([]domain.CodificationCandidate(nil), candidates...),
	}
}

func groupInsights(insights []domain.ObjectiveInsight) []Group {
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
