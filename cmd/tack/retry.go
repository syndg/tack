package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/domain"
)

var retryGuidance string
var retryScopeAdditions []string

func init() {
	retryCmd.Flags().StringVar(&retryGuidance, "guidance", "", "human guidance for the retry")
	retryCmd.Flags().StringArrayVar(&retryScopeAdditions, "scope", nil, "additional file scope allowed for this retry; repeatable")
	rootCmd.AddCommand(retryCmd)
}

var retryCmd = &cobra.Command{
	Use:   "retry [run-id] [stream-id]",
	Short: "Retry a failed stream with optional human guidance",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newDaemonClient(cmd, true)
		if err != nil {
			return err
		}
		if _, err := c.RunCommand(cmd.Context(), args[0], domain.Command{
			Kind:           domain.CommandRetry,
			StreamID:       args[1],
			Guidance:       retryGuidance,
			ScopeAdditions: retryScopeAdditions,
		}); err != nil {
			return err
		}
		fmt.Printf("Retry started for stream %s.\n", args[1])
		return nil
	},
}
