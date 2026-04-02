package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

var (
	planSimple    bool
	planBlueprint string
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
			fmt.Printf("Plan %s auto-approved. Execution started.\n", result.Plan.ID)
		} else {
			obj, err := c.CreateObjectiveWithOptions(cmd.Context(), args[0], client.CreateObjectiveOptions{
				Blueprint: planBlueprint,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
			fmt.Println("Planner agent will decompose the objective.")
		}
		return nil
	},
}

func init() {
	planCmd.Flags().BoolVar(&planSimple, "simple", false, "single-agent mode (no decomposition)")
	planCmd.Flags().StringVar(&planBlueprint, "blueprint", "", "blueprint to use (default: auto-detect)")
	rootCmd.AddCommand(planCmd)
}
