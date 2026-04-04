package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List active agent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		sessions, err := c.ListAgents(cmd.Context())
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tROLE\tOBJECTIVE\tSTREAM\tSANDBOX\tSTATUS\tCREATED")
		for _, s := range sessions {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				truncateID(s.ID),
				string(s.Role),
				truncateID(s.ObjectiveID),
				truncateID(s.StreamID),
				truncateID(s.SandboxID),
				s.Status,
				timeAgo(s.CreatedAt),
			)
		}
		return w.Flush()
	},
}

var killAgentCmd = &cobra.Command{
	Use:   "kill [agent-id]",
	Short: "Terminate an active agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		err = c.KillAgent(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Agent %s terminated.\n", args[0])
		return nil
	},
}

func init() {
	agentsCmd.AddCommand(killAgentCmd)
	rootCmd.AddCommand(agentsCmd)
}
