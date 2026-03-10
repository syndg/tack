package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/client"
)

var (
	planSimple    bool
	planBlueprint string
	planAuto      bool
)

var planCmd = &cobra.Command{
	Use:   "plan [description]",
	Short: "Create a new objective and plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)

		if planSimple {
			result, err := c.CreateObjectiveSimple(cmd.Context(), args[0], planBlueprint)
			if err != nil {
				return err
			}
			fmt.Printf("Created objective %s in simple mode.\n", result.Objective.ID)
			fmt.Printf("Plan %s auto-approved. Ready for execution.\n", result.Plan.ID)
		} else {
			obj, err := c.CreateObjectiveWithOptions(cmd.Context(), args[0], client.CreateObjectiveOptions{
				Blueprint: planBlueprint,
				Auto:      planAuto,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
			if planAuto {
				fmt.Println("Planner will run in batch mode.")
			} else {
				fmt.Println("Planner will start an interactive session.")
			}
		}
		return nil
	},
}

func init() {
	planCmd.Flags().BoolVar(&planSimple, "simple", false, "single-agent mode (no decomposition)")
	planCmd.Flags().StringVar(&planBlueprint, "blueprint", "", "blueprint to use (default: auto-detect)")
	planCmd.Flags().BoolVar(&planAuto, "auto", false, "batch mode (planner runs autonomously)")
	rootCmd.AddCommand(planCmd)
}
