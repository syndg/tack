package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

func init() {
	rootCmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show [plan-id]",
	Short: "Show plan details with streams",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		resp, err := c.GetPlan(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		plan := resp.Plan
		streams := resp.Streams

		// Build stream ID → 1-based index for dependency labels.
		idToIdx := make(map[string]int, len(streams))
		for i, s := range streams {
			idToIdx[s.ID] = i + 1
		}

		fmt.Printf("Plan:          %s\n", truncateID(plan.ID))
		fmt.Printf("Objective:     %s\n", truncateID(plan.ObjectiveID))
		fmt.Printf("Status:        %s\n", plan.Status)
		if len(plan.QualityGates) > 0 {
			fmt.Printf("Quality Gates: %s\n", strings.Join(plan.QualityGates, ", "))
		} else {
			fmt.Printf("Quality Gates: none\n")
		}

		if len(streams) == 0 {
			fmt.Println("\nStreams: none")
			return nil
		}

		fmt.Println("\nStreams:")
		for i, s := range streams {
			fmt.Printf("  %d. %-50s [%s]\n", i+1, s.Title, s.Status)

			if len(s.FileScope) > 0 {
				fmt.Printf("     Scope: %s\n", strings.Join(s.FileScope, ", "))
			}

			if len(s.Dependencies) == 0 {
				fmt.Printf("     Dependencies: none\n")
			} else {
				deps := make([]string, 0, len(s.Dependencies))
				for _, depID := range s.Dependencies {
					if idx, ok := idToIdx[depID]; ok {
						deps = append(deps, fmt.Sprintf("stream %d", idx))
					} else {
						deps = append(deps, truncateID(depID))
					}
				}
				fmt.Printf("     Dependencies: %s\n", strings.Join(deps, ", "))
			}

			fmt.Println()
		}

		return nil
	},
}
