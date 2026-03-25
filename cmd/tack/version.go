package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the tack version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("tack", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
