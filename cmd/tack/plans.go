package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/domain"
)

func init() {
	rootCmd.AddCommand(plansCmd)
}

var plansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List plans",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		plans, err := c.ListPlans(cmd.Context())
		if err != nil {
			return err
		}
		objectives, err := c.ListObjectives(cmd.Context())
		if err != nil {
			return err
		}
		objectiveByID := objectivesByID(objectives)

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "PLAN\tSTATUS\tCREATED\tOBJECTIVE")
		for _, p := range plans {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				truncateID(p.ID),
				string(p.Status),
				timeAgo(p.CreatedAt),
				objectiveLabel(p, objectiveByID),
			)
		}
		return w.Flush()
	},
}

func objectivesByID(objectives []domain.Objective) map[string]domain.Objective {
	byID := make(map[string]domain.Objective, len(objectives))
	for _, obj := range objectives {
		byID[obj.ID] = obj
	}
	return byID
}

func objectiveLabel(plan domain.Plan, objectiveByID map[string]domain.Objective) string {
	if obj, ok := objectiveByID[plan.ObjectiveID]; ok {
		return truncateObjectiveText(obj.Description, 64)
	}
	return truncateID(plan.ObjectiveID)
}

func truncateID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

func truncateObjectiveText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return strings.TrimRight(s[:max-3], " ") + "..."
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
