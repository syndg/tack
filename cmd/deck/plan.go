package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/client"
)

func init() {
	rootCmd.AddCommand(planCmd)
}

var planCmd = &cobra.Command{
	Use:   "plan [description]",
	Short: "Create a new objective",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		obj, err := c.CreateObjective(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
		return nil
	},
}
