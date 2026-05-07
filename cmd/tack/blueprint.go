package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/blueprintconfig"
	"github.com/syndg/tack/internal/harness/blueprint"
)

var blueprintRemove []string
var blueprintSavePath string

func init() {
	blueprintRequirementsCmd.Flags().StringSliceVar(&blueprintRemove, "remove", nil, "step ID to exclude from the candidate; repeat or comma-separate")
	blueprintSaveGlobalCmd.Flags().StringSliceVar(&blueprintRemove, "remove", nil, "step ID to exclude from the saved candidate; repeat or comma-separate")
	blueprintSaveGlobalCmd.Flags().StringVar(&blueprintSavePath, "path", "", "override output path")
	blueprintCmd.AddCommand(blueprintStepsCmd, blueprintRequirementsCmd, blueprintSaveGlobalCmd)
	rootCmd.AddCommand(blueprintCmd)
}

var blueprintCmd = &cobra.Command{
	Use:   "blueprint",
	Short: "Inspect and configure blueprint steps",
}

var blueprintStepsCmd = &cobra.Command{
	Use:   "steps",
	Short: "Show shipped standard blueprint steps",
	RunE: func(cmd *cobra.Command, args []string) error {
		bp, err := blueprintconfig.LoadShippedStandard()
		if err != nil {
			return err
		}
		for _, row := range blueprintconfig.Rows(bp) {
			parts := []string{fmt.Sprintf("id=%s", row.ID), fmt.Sprintf("type=%s", row.Type)}
			if row.Role != "" {
				parts = append(parts, "role="+row.Role)
			}
			if row.Action != "" {
				parts = append(parts, "action="+row.Action)
			}
			if row.Ref != "" {
				parts = append(parts, "ref="+row.Ref)
			}
			parts = append(parts, listPart("dependencies", row.Dependencies))
			parts = append(parts, listPart("provides", row.Provides))
			parts = append(parts, listPart("requirements", row.Requirements))
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(parts, " ")); err != nil {
				return err
			}
		}
		return nil
	},
}

var blueprintRequirementsCmd = &cobra.Command{
	Use:   "requirements",
	Short: "Show requirements for a standard blueprint candidate",
	RunE: func(cmd *cobra.Command, args []string) error {
		candidate, err := standardCandidate(blueprintRemove)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(blueprintconfig.ExtractRequirements(candidate))
	},
}

var blueprintSaveGlobalCmd = &cobra.Command{
	Use:   "save-global",
	Short: "Save one global standard blueprint override",
	RunE: func(cmd *cobra.Command, args []string) error {
		candidate, err := standardCandidate(blueprintRemove)
		if err != nil {
			return err
		}
		path := blueprintSavePath
		if path == "" {
			path = globalBlueprintOverridePath()
		}
		if err := blueprintconfig.SaveGlobalOverride(path, candidate); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "saved %s\n", path)
		return err
	},
}

func standardCandidate(remove []string) (*blueprint.Blueprint, error) {
	bp, err := blueprintconfig.LoadShippedStandard()
	if err != nil {
		return nil, err
	}
	return blueprintconfig.CandidateFromRemoved(bp, remove)
}

func listPart(name string, values []string) string {
	if len(values) == 0 {
		return name + "=[]"
	}
	return name + "=[" + strings.Join(values, ",") + "]"
}

func globalBlueprintOverridePath() string {
	if userCfg := userConfigPath(); userCfg != "" && userCfg != "~/.config/tack/config.yaml" {
		return filepath.Join(filepath.Dir(expandPath(userCfg)), "blueprints", "standard.yaml")
	}
	return filepath.Join(expandPath("~/.config/tack"), "blueprints", "standard.yaml")
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return os.ExpandEnv(path)
}
