package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/runtimeauth"
	"github.com/syndg/tack/internal/runtimecatalog"
	"github.com/syndg/tack/internal/validation"
)

var doctorJSON bool

var doctorRuntimeRunner runtimecatalog.Runner = runtimecatalog.ExecRunner{}
var doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi

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
		{Name: "runtime_auth", Run: checkRuntimeAuth},
		{Name: "pi_runtime", Run: checkPIRuntime},
		{Name: "pi_daemon_visibility", Run: checkPIDaemonVisibility},
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

func checkRuntimeAuth(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	binding := cfg.EffectiveRuntimeAuth()
	adapter, err := runtimeauth.New(binding.Runtime)
	if err != nil {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "effective_config",
			Evidence: err.Error(),
			Fix:      "set agents.runtime to a supported runtime",
		}}, nil
	}
	if err := runtimeauth.ValidateBinding(adapter, binding, cfg.Sandbox.Provider); err != nil {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "effective_config",
			Evidence: err.Error(),
			Fix:      "set runtime_auth provider, method, and credential_ref to compatible values",
		}}, nil
	}
	if binding.Mode == runtimeauth.ModeNative {
		return []validation.Finding{{Status: validation.StatusPass, Source: "effective_config", Evidence: fmt.Sprintf("runtime_auth.mode=native provider=%s", binding.Provider)}}, nil
	}
	store, err := loadCredentialsStore()
	if err != nil {
		return nil, err
	}
	report, err := runtimeauth.ResolveCredentialBinding(binding, store)
	if err != nil {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "credentials",
			Evidence: err.Error(),
			Fix:      "run tack auth add " + binding.Provider,
		}}, nil
	}
	return []validation.Finding{{
		Status:   validation.StatusPass,
		Source:   report.Source,
		Evidence: fmt.Sprintf("provider=%s method=%s credential_ref=%s credential_type=%s", report.Provider, report.Method, report.CredentialRef, report.CredentialType),
	}}, nil
}

func checkPIRuntime(ctx context.Context) ([]validation.Finding, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Agents.Runtime != "pi" {
		return []validation.Finding{{Status: validation.StatusSkip, Source: "effective_config", Evidence: "agents.runtime=" + cfg.Agents.Runtime}}, nil
	}
	probe := runtimecatalog.ProbePi(ctx, doctorRuntimeRunner)
	if !probe.Installed {
		fix := "install Pi or select a different runtime"
		if len(probe.PackageManagers) == 0 {
			fix = "install Node.js/npm or Bun before installing Pi, or select a different runtime"
		}
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "environment",
			Evidence: "pi executable was not found in PATH",
			Fix:      fix,
			Details: map[string]string{
				"package_managers": strings.Join(probe.PackageManagers, ","),
			},
		}}, nil
	}
	if probe.CatalogError != "" {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "pi",
			Evidence: probe.CatalogError,
			Fix:      "repair Pi installation or choose models after pi --list-models succeeds",
		}}, nil
	}
	models := 0
	for _, provider := range probe.Catalog {
		models += len(provider.Models)
	}
	return []validation.Finding{{
		Status:   validation.StatusPass,
		Source:   "environment",
		Evidence: fmt.Sprintf("pi=%s providers=%d models=%d", probe.Path, len(probe.Catalog), models),
	}}, nil
}

func checkPIDaemonVisibility(ctx context.Context) ([]validation.Finding, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Agents.Runtime != "pi" {
		return []validation.Finding{{Status: validation.StatusSkip, Source: "effective_config", Evidence: "agents.runtime=" + cfg.Agents.Runtime}}, nil
	}
	ok, evidence := doctorDaemonCanSeePi(ctx, cfg.Daemon.Listen)
	if !ok {
		return []validation.Finding{{
			Status:   validation.StatusWarn,
			Source:   "daemon_service",
			Evidence: evidence,
			Fix:      "start or reload the daemon service after Pi is available in the service environment",
		}}, nil
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: "daemon_service", Evidence: evidence}}, nil
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
