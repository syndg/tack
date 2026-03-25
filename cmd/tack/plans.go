package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

func init() {
	rootCmd.AddCommand(plansCmd)
}

var plansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List plans",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		plans, err := c.ListPlans(cmd.Context())
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tOBJECTIVE\tSTATUS\tCREATED")
		for _, p := range plans {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				truncateID(p.ID),
				truncateID(p.ObjectiveID),
				string(p.Status),
				timeAgo(p.CreatedAt),
			)
		}
		return w.Flush()
	},
}

func truncateID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
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
