package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(execCmd)
}

var execCmd = &cobra.Command{
	Use:   "exec [objective-id]",
	Short: "Trigger execution for an approved objective",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		err = c.ExecuteObjective(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Execution started for objective %s.\n", args[0])
		return nil
	},
}
