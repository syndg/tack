package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

var planBlueprint string

var planCmd = &cobra.Command{
	Use:   "plan [description]",
	Short: "Create a new objective",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		obj, err := c.CreateObjectiveWithOptions(cmd.Context(), args[0], client.CreateObjectiveOptions{
			Blueprint: planBlueprint,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
		fmt.Println("Execution started.")
		return nil
	},
}

func init() {
	planCmd.Flags().StringVar(&planBlueprint, "blueprint", "", "blueprint to use (default: project default or only loaded default blueprint)")
	rootCmd.AddCommand(planCmd)
}
