package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/validation"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate Tack readiness",
	RunE: func(cmd *cobra.Command, args []string) error {
		report := validation.Run(cmd.Context(), doctorChecks()...)
		if doctorJSON {
			return validation.RenderJSON(cmd.OutOrStdout(), report)
		}
		return validation.RenderHuman(cmd.OutOrStdout(), report)
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "render validation findings as JSON")
	rootCmd.AddCommand(doctorCmd)
}

func doctorChecks() []validation.Check {
	return []validation.Check{
		{Name: "project_config", Run: checkProjectConfig},
		{Name: "user_config", Run: checkUserConfig},
		{Name: "daemon_config", Run: checkDaemonConfig},
	}
}

func checkProjectConfig(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	projectPath, err := config.ResolveProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	if projectPath == "" {
		return []validation.Finding{{
			Status:   validation.StatusWarn,
			Source:   "project",
			Evidence: "no .tack/config.yaml found from current directory",
			Fix:      "run tack init from a project repository",
		}}, nil
	}
	if _, err := os.Stat(projectPath); err != nil {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "project",
			Evidence: fmt.Sprintf("%s: %v", projectPath, err),
			Fix:      "create or repair the project config with tack init",
		}}, nil
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: "project", Evidence: projectPath}}, nil
}

func checkUserConfig(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	path := userConfigPath()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return []validation.Finding{{
				Status:   validation.StatusWarn,
				Source:   "global",
				Evidence: fmt.Sprintf("%s does not exist", path),
				Fix:      "run tack setup or create a user config",
			}}, nil
		}
		return nil, err
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: "global", Evidence: path}}, nil
}

func checkDaemonConfig(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	listen := strings.TrimSpace(cfg.Daemon.Listen)
	if listen == "" {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "effective_config",
			Evidence: "daemon.listen is empty",
			Fix:      "set daemon.listen in global or project config",
		}}, nil
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: "effective_config", Evidence: "daemon.listen=" + listen}}, nil
}
