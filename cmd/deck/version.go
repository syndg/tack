package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the deck version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("deck", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
