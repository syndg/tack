package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

func init() {
	rootCmd.AddCommand(approveCmd)
	rootCmd.AddCommand(rejectCmd)
}

var approveCmd = &cobra.Command{
	Use:   "approve [plan-id]",
	Short: "Approve a plan for execution",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		if err := c.ApprovePlan(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Plan %s approved. Execution will begin.\n", args[0])
		return nil
	},
}

var rejectCmd = &cobra.Command{
	Use:   "reject [plan-id]",
	Short: "Reject a plan and return to planning",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		if err := c.RejectPlan(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Plan %s rejected. Objective returned to planning.\n", args[0])
		return nil
	},
}
