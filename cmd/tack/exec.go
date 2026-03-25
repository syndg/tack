package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
)

func init() {
	rootCmd.AddCommand(execCmd)
}

var execCmd = &cobra.Command{
	Use:   "exec [objective-id]",
	Short: "Trigger execution for an approved objective",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := client.New(daemonURL)
		err := c.ExecuteObjective(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Execution started for objective %s.\n", args[0])
		return nil
	},
}
