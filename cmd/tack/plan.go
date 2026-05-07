package main

import (
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

var planBlueprint string

var planCmd = &cobra.Command{
	Use:   "plan [description]",
	Short: "Create a new objective",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		obj, err := c.CreateObjectiveWithOptions(cmd.Context(), args[0], client.CreateObjectiveOptions{
			Blueprint: planBlueprint,
		})
		if err != nil {
			return err
		}
		cmd.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
		cmd.Println("Planning started. Run `tack show` to inspect the latest plan, then `tack approve` to continue.")
		return nil
	},
}

func init() {
	planCmd.Flags().StringVar(&planBlueprint, "blueprint", "", "blueprint id to use for this objective")
	rootCmd.AddCommand(planCmd)
}
