package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var mergeObjective string

func init() {
	mergeCmd.Flags().StringVar(&mergeObjective, "objective", "", "filter by objective ID")
	mergeCmd.AddCommand(mergeRetryCmd)
	mergeCmd.AddCommand(mergeDiffCmd)
	rootCmd.AddCommand(mergeCmd)
}

var mergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "View merge queue status",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		entries, err := c.ListMergeQueue(cmd.Context(), mergeObjective)
		if err != nil {
			return err
		}

		if len(entries) == 0 {
			fmt.Println("No merge queue entries.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tSTREAM\tBRANCH\tSTATUS\tTIER\tCREATED")
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
				truncateID(e.ID),
				truncateID(e.StreamID),
				e.Branch,
				string(e.Status),
				e.Tier,
				timeAgo(time.Unix(e.CreatedAt, 0)),
			)
		}
		return w.Flush()
	},
}

var mergeRetryCmd = &cobra.Command{
	Use:   "retry [entry-id]",
	Short: "Retry a failed merge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		if err := c.RetryMerge(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Merge entry %s re-queued.\n", args[0])
		return nil
	},
}

var mergeDiffCmd = &cobra.Command{
	Use:   "diff [stream-id]",
	Short: "View diff for a merged stream",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		diff, err := c.GetStreamDiff(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Diff for stream %s:\n", args[0])
		fmt.Printf("  %d files changed, %d insertions(+), %d deletions(-)\n\n",
			diff.FilesChanged, diff.Insertions, diff.Deletions)

		for _, f := range diff.Files {
			status := statusLetter(f.Status)
			fmt.Printf("  %s  %-30s (+%d, -%d)\n", status, f.Path, f.Insertions, f.Deletions)
		}
		return nil
	},
}

// statusLetter maps a file diff status to a single-letter indicator.
func statusLetter(status string) string {
	switch status {
	case "added":
		return "A"
	case "modified":
		return "M"
	case "deleted":
		return "D"
	case "renamed":
		return "R"
	default:
		return "?"
	}
}
