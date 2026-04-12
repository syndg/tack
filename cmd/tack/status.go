package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		url := effectiveDaemonURL()
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		status, err := c.GetStatus(cmd.Context())
		if err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Daemon not reachable at %s\n", url)
			return err
		}

		fmt.Println("Tack Daemon Status")
		fmt.Printf("  %-12s %s\n", "URL:", url)
		fmt.Printf("  %-12s %s\n", "Uptime:", status.Uptime)

		if len(status.Objectives) > 0 {
			fmt.Println("  Objectives:")

			// Sort keys for consistent output
			keys := make([]string, 0, len(status.Objectives))
			for k := range status.Objectives {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				fmt.Printf("    %-14s %d\n", k+":", status.Objectives[k])
			}
		}

		return nil
	},
}
