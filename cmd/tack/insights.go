package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
)

var insightsJSON bool
var insightPromotionTarget string

func init() {
	insightsCmd.Flags().BoolVar(&insightsJSON, "json", false, "print machine-readable JSON")
	insightsPromoteCmd.Flags().StringVar(&insightPromotionTarget, "target", "", "promotion target: project-memory or codification")
	insightsCmd.AddCommand(insightsShowCmd, insightsPromoteCmd, insightsRejectCmd)
	rootCmd.AddCommand(insightsCmd)
}

var insightsCmd = &cobra.Command{
	Use:   "insights [objective-id|latest]",
	Short: "Show objective insights and codification candidates",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		ref := ""
		if len(args) > 0 {
			ref = args[0]
		}
		objective, err := resolveObjectiveRef(cmd, c, ref)
		if err != nil {
			return err
		}
		report, err := c.GetObjectiveInsightReport(cmd.Context(), objective.ID)
		if err != nil {
			return err
		}
		if insightsJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		formatInsightReport(cmd.OutOrStdout(), *objective, *report)
		return nil
	},
}

var insightsShowCmd = &cobra.Command{
	Use:   "show <insight-or-candidate-id>",
	Short: "Show one insight or codification candidate",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		detail, err := c.GetInsightDetail(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if insightsJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(detail)
		}
		formatInsightDetail(cmd.OutOrStdout(), *detail)
		return nil
	},
}

var insightsPromoteCmd = &cobra.Command{
	Use:   "promote <insight-or-candidate-id>",
	Short: "Approve an insight or codification candidate promotion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		target := domain.PromotionTarget(strings.TrimSpace(insightPromotionTarget))
		record, err := c.PromoteInsightSource(cmd.Context(), args[0], target)
		if err != nil {
			return err
		}
		if insightsJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(record)
		}
		formatPromotionRecord(cmd.OutOrStdout(), "Approved", *record)
		return nil
	},
}

var insightsRejectCmd = &cobra.Command{
	Use:   "reject <insight-or-candidate-id>",
	Short: "Reject an insight or codification candidate promotion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		record, err := c.RejectInsightSource(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if insightsJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(record)
		}
		formatPromotionRecord(cmd.OutOrStdout(), "Rejected", *record)
		return nil
	},
}

func formatInsightDetail(w io.Writer, detail insightreport.Detail) {
	switch detail.Kind {
	case insightreport.DetailKindInsight:
		if detail.Insight == nil {
			fmt.Fprintln(w, "Insight: missing")
			return
		}
		formatRawInsightDetail(w, *detail.Insight)
	case insightreport.DetailKindCandidate:
		if detail.Candidate == nil {
			fmt.Fprintln(w, "Candidate: missing")
			return
		}
		formatCandidateDetail(w, *detail.Candidate)
	default:
		fmt.Fprintf(w, "Unknown insight detail kind: %s\n", detail.Kind)
	}
}

func formatRawInsightDetail(w io.Writer, insight domain.ObjectiveInsight) {
	fmt.Fprintf(w, "Insight:   %s\n", insight.ID)
	fmt.Fprintf(w, "Objective: %s\n", insight.ObjectiveID)
	if insight.StreamID != "" {
		fmt.Fprintf(w, "Stream:    %s\n", insight.StreamID)
	}
	if insight.PlanID != "" {
		fmt.Fprintf(w, "Plan:      %s\n", insight.PlanID)
	}
	if insight.ExecutionID != "" {
		fmt.Fprintf(w, "Execution: %s\n", insight.ExecutionID)
	}
	fmt.Fprintf(w, "Source:    %s\n", insight.Source)
	fmt.Fprintf(w, "Kind:      %s\n", insight.Kind)
	fmt.Fprintf(w, "Created:   %s\n", formatReportTime(insight.CreatedAt))
	if insight.Summary != "" {
		fmt.Fprintf(w, "Summary:   %s\n", insight.Summary)
	}
	if insight.Detail != "" {
		fmt.Fprintf(w, "Detail:    %s\n", insight.Detail)
	}
	formatPayload(w, insight.Payload)
}

func formatCandidateDetail(w io.Writer, candidate domain.CodificationCandidate) {
	fmt.Fprintf(w, "Candidate: %s\n", candidate.ID)
	fmt.Fprintf(w, "Objective: %s\n", candidate.ObjectiveID)
	fmt.Fprintf(w, "Target:    %s\n", candidate.Target)
	fmt.Fprintf(w, "Status:    %s\n", candidate.Status)
	fmt.Fprintf(w, "Evidence:  %d\n", candidate.EvidenceCount)
	fmt.Fprintf(w, "Created:   %s\n", formatReportTime(candidate.CreatedAt))
	fmt.Fprintf(w, "Updated:   %s\n", formatReportTime(candidate.UpdatedAt))
	if candidate.Title != "" {
		fmt.Fprintf(w, "Title:     %s\n", candidate.Title)
	}
	if candidate.Instruction != "" {
		fmt.Fprintf(w, "Instruction:\n%s\n", candidate.Instruction)
	}
	if candidate.Rationale != "" {
		fmt.Fprintf(w, "Rationale:\n%s\n", candidate.Rationale)
	}
	formatPayload(w, candidate.Payload)
}

func formatPayload(w io.Writer, payload map[string]string) {
	if len(payload) == 0 {
		return
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "Payload:")
	for _, key := range keys {
		fmt.Fprintf(w, "  %s: %s\n", key, payload[key])
	}
}

func formatInsightReport(w io.Writer, objective domain.Objective, report insightreport.Report) {
	fmt.Fprintf(w, "Objective:  %s\n", report.ObjectiveID)
	if objective.Description != "" {
		fmt.Fprintf(w, "Summary:    %s\n", truncateObjectiveText(objective.Description, 90))
	}
	fmt.Fprintf(w, "Insights:   %d across %d groups\n", report.Summary.TotalInsights, report.Summary.TotalGroups)
	fmt.Fprintf(w, "Candidates: %d\n", report.Summary.Candidates)

	if len(report.Groups) == 0 {
		fmt.Fprintln(w, "\nInsight Groups: none")
	} else {
		fmt.Fprintln(w, "\nInsight Groups:")
		for _, group := range report.Groups {
			stream := "objective"
			if group.StreamID != "" {
				stream = truncateID(group.StreamID)
			}
			fmt.Fprintf(w, "  - %s/%s on %s: %d insight(s), %s..%s\n", group.Kind, group.Source, stream, group.Count, formatReportTime(group.FirstSeen), formatReportTime(group.LastSeen))
			for _, summary := range group.Summaries[:min(2, len(group.Summaries))] {
				fmt.Fprintf(w, "    %s\n", truncateObjectiveText(summary, 100))
			}
		}
	}

	if len(report.Candidates) == 0 {
		fmt.Fprintln(w, "\nCodification Candidates: none")
		return
	}
	fmt.Fprintln(w, "\nCodification Candidates:")
	metadataByCandidate := map[string]insightreport.CandidateMetadata{}
	for _, metadata := range report.CandidateMetadata {
		metadataByCandidate[metadata.CandidateID] = metadata
	}
	for _, candidate := range report.Candidates {
		parts := []string{string(candidate.Target), string(candidate.Status), fmt.Sprintf("evidence=%d", candidate.EvidenceCount)}
		if metadata, ok := metadataByCandidate[candidate.ID]; ok {
			parts = append(parts, fmt.Sprintf("confidence=%.2f", metadata.Confidence))
			if metadata.ThresholdEligible {
				parts = append(parts, "threshold=eligible")
			}
			if !metadata.AutoApprovalEnabled {
				parts = append(parts, "auto-approval=off")
			}
		}
		fmt.Fprintf(w, "  - %s [%s]\n", candidate.ID, strings.Join(parts, ", "))
		fmt.Fprintf(w, "    %s\n", truncateObjectiveText(candidate.Instruction, 100))
	}
}

func formatPromotionRecord(w io.Writer, action string, record domain.PromotionRecord) {
	fmt.Fprintf(w, "%s promotion: %s\n", action, record.ID)
	source := record.SourceCandidateID
	if source == "" && len(record.SourceInsightIDs) > 0 {
		source = strings.Join(record.SourceInsightIDs, ",")
	}
	fmt.Fprintf(w, "Source: %s\n", source)
	fmt.Fprintf(w, "Target: %s\n", record.Target)
	fmt.Fprintf(w, "Status: %s\n", record.Status)
	if record.SupportCount > 0 {
		fmt.Fprintf(w, "Support: %d confidence=%.2f\n", record.SupportCount, record.Confidence)
	}
	if record.Summary != "" {
		fmt.Fprintf(w, "Summary: %s\n", truncateObjectiveText(record.Summary, 100))
	}
}

func formatReportTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04")
}
