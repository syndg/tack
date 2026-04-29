package main

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(approveCmd)
	rootCmd.AddCommand(rejectCmd)
}

var approveCmd = &cobra.Command{
	Use:   "approve [plan-id|objective-id|latest]",
	Short: "Approve a plan for execution",
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
		plan, err := resolvePlanRef(cmd, c, ref, planResolveOptions{preferPendingApproval: true})
		if err != nil {
			return err
		}
		if err := c.ApprovePlan(cmd.Context(), plan.ID); err != nil {
			return err
		}
		cmd.Printf("Plan %s approved. Execution will begin.\n", plan.ID)
		return nil
	},
}

var rejectCmd = &cobra.Command{
	Use:   "reject [plan-id|objective-id|latest]",
	Short: "Reject a plan and return to planning",
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
		plan, err := resolvePlanRef(cmd, c, ref, planResolveOptions{preferPendingApproval: true})
		if err != nil {
			return err
		}
		if err := c.RejectPlan(cmd.Context(), plan.ID); err != nil {
			return err
		}
		cmd.Printf("Plan %s rejected. Objective returned to planning.\n", plan.ID)
		return nil
	},
}
