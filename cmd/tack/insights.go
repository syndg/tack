package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
)

var insightsJSON bool

func init() {
	insightsCmd.Flags().BoolVar(&insightsJSON, "json", false, "print machine-readable JSON")
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
	for _, candidate := range report.Candidates {
		parts := []string{string(candidate.Target), string(candidate.Status), fmt.Sprintf("evidence=%d", candidate.EvidenceCount)}
		fmt.Fprintf(w, "  - %s [%s]\n", candidate.ID, strings.Join(parts, ", "))
		fmt.Fprintf(w, "    %s\n", truncateObjectiveText(candidate.Instruction, 100))
	}
}

func formatReportTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04")
}
