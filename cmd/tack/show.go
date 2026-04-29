package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show [plan-id|objective-id|latest]",
	Short: "Show plan details with streams",
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
		planMeta, err := resolvePlanRef(cmd, c, ref, planResolveOptions{preferPendingApproval: true})
		if err != nil {
			return err
		}
		resp, err := c.GetPlan(cmd.Context(), planMeta.ID)
		if err != nil {
			return err
		}

		plan := resp.Plan
		streams := resp.Streams
		objective, err := c.GetObjective(cmd.Context(), plan.ObjectiveID)
		if err != nil {
			return err
		}

		// Build stream ID → 1-based index for dependency labels.
		idToIdx := make(map[string]int, len(streams))
		for i, s := range streams {
			idToIdx[s.ID] = i + 1
		}

		cmd.Printf("Plan:          %s\n", plan.ID)
		cmd.Printf("Objective:     %s\n", plan.ObjectiveID)
		cmd.Printf("Description:   %s\n", objective.Description)
		cmd.Printf("Status:        %s\n", plan.Status)
		if len(plan.QualityGates) > 0 {
			cmd.Printf("Quality Gates: %s\n", strings.Join(plan.QualityGates, ", "))
		} else {
			cmd.Printf("Quality Gates: none\n")
		}

		if len(streams) == 0 {
			cmd.Println("\nStreams: none")
			return nil
		}

		cmd.Println("\nStreams:")
		for i, s := range streams {
			cmd.Printf("  %d. %-50s [%s]\n", i+1, s.Title, s.Status)

			if len(s.FileScope) > 0 {
				cmd.Printf("     Scope: %s\n", strings.Join(s.FileScope, ", "))
			}

			if len(s.Dependencies) == 0 {
				cmd.Printf("     Dependencies: none\n")
			} else {
				deps := make([]string, 0, len(s.Dependencies))
				for _, depID := range s.Dependencies {
					if idx, ok := idToIdx[depID]; ok {
						deps = append(deps, fmt.Sprintf("stream %d", idx))
					} else {
						deps = append(deps, truncateID(depID))
					}
				}
				cmd.Printf("     Dependencies: %s\n", strings.Join(deps, ", "))
			}

			cmd.Println()
		}

		return nil
	},
}
