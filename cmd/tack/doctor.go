package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/preflight"
	"github.com/syndg/tack/internal/runtimeauth"
	"github.com/syndg/tack/internal/runtimecatalog"
	"github.com/syndg/tack/internal/validation"
)

var doctorJSON bool

var doctorRuntimeRunner runtimecatalog.Runner = runtimecatalog.ExecRunner{}
var doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
var doctorGitRemote func(context.Context, string) (string, error)
var doctorDaemonServiceProvider = newDaemonServiceProvider

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
		{Name: "global_setup", Run: checkGlobalSetup},
		{Name: "effective_config", Run: checkEffectiveConfig},
		{Name: "daemon_config", Run: checkDaemonConfig},
		{Name: "daemon_service", Run: checkDaemonService},
		{Name: "runtime_auth", Run: checkRuntimeAuth},
		{Name: "blueprint_preflight", Run: checkBlueprintPreflight},
		{Name: "pi_runtime", Run: checkPIRuntime},
		{Name: "pi_daemon_visibility", Run: checkPIDaemonVisibility},
	}
}

func checkBlueprintPreflight(ctx context.Context) ([]validation.Finding, error) {
	projectPath, err := config.ResolveProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	if projectPath == "" {
		return []validation.Finding{{Status: validation.StatusSkip, Source: "project", Evidence: "no project config found"}}, nil
	}

	effective, err := config.ResolveEffective(projectPath, userConfigPath())
	if err != nil {
		return nil, err
	}
	if !effective.Blueprint.Set {
		return []validation.Finding{{Status: validation.StatusSkip, Source: string(config.ValueSourceMissing), Evidence: "blueprint is not set"}}, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	reg := blueprint.NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		return nil, fmt.Errorf("loading shipped blueprints: %w", err)
	}
	if _, ok := reg.Get(effective.Blueprint.Value); !ok {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   string(effective.Blueprint.Source),
			Evidence: fmt.Sprintf("blueprint=%s", effective.Blueprint.Value),
			Fix:      "select a shipped blueprint or repair the project blueprint override",
		}}, nil
	}
	store, err := loadCredentialsStore()
	if err != nil {
		return nil, err
	}
	checker := preflight.New(preflight.Options{
		Blueprints:               registryBlueprintLookup{reg: reg},
		ProjectRoot:              effective.ProjectRoot,
		Credentials:              store,
		RuntimeAuthMode:          valueOrEmpty(effective.AuthMode),
		RuntimeAuthProvider:      valueOrEmpty(effective.Provider),
		RuntimeAuthMethod:        valueOrEmpty(effective.AuthMethod),
		RuntimeAuthCredentialRef: valueOrEmpty(effective.CredentialRef),
		SandboxProvider:          valueOrEmpty(effective.SandboxProvider),
		DaemonExternalURL:        cfg.Daemon.ExternalURL,
		GitRemote:                doctorGitRemote,
	})
	if err := checker.Check(ctx, effective.Blueprint.Value); err != nil {
		var failure preflight.Failure
		if errors.As(err, &failure) {
			return failure.Findings(), nil
		}
		return nil, err
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: string(effective.Blueprint.Source), Evidence: fmt.Sprintf("blueprint=%s requirements satisfied", effective.Blueprint.Value)}}, nil
}

func checkDaemonService(ctx context.Context) ([]validation.Finding, error) {
	provider, err := doctorDaemonServiceProvider()
	if err != nil {
		return []validation.Finding{{
			Status:   validation.StatusWarn,
			Source:   "daemon_service",
			Evidence: err.Error(),
			Fix:      "use foreground daemon mode or configure a supported macOS launchd or Linux systemd user service",
		}}, nil
	}
	status, err := provider.Status(ctx)
	if err != nil {
		return []validation.Finding{{
			Status:   validation.StatusFail,
			Source:   "daemon_service",
			Evidence: err.Error(),
			Fix:      "run tack daemon status or repair the daemon user service",
		}}, nil
	}

	parts := []string{
		"provider=" + status.Provider,
		fmt.Sprintf("installed=%t", status.Installed),
		fmt.Sprintf("enabled=%t", status.Enabled),
		fmt.Sprintf("running=%t", status.Running),
		fmt.Sprintf("healthy=%t", status.Healthy),
	}
	if status.Listen != "" {
		parts = append(parts, "listen="+status.Listen)
	}
	if status.ConfigPath != "" {
		parts = append(parts, "config="+status.ConfigPath)
	}
	if status.LogPath != "" {
		parts = append(parts, "logs="+status.LogPath)
	}
	if len(status.Details) > 0 {
		parts = append(parts, "details="+strings.Join(status.Details, " | "))
	}

	if !status.Installed || !status.Enabled || !status.Running || !status.Healthy {
		return []validation.Finding{{
			Status:   validation.StatusWarn,
			Source:   "daemon_service",
			Evidence: strings.Join(parts, " "),
			Fix:      "run tack daemon install/start/status or use foreground daemon mode for development",
		}}, nil
	}
	return []validation.Finding{{Status: validation.StatusPass, Source: "daemon_service", Evidence: strings.Join(parts, " ")}}, nil
}

func valueOrEmpty(value config.EffectiveString) string {
	if !value.Set {
		return ""
	}
	return value.Value
}

func checkEffectiveConfig(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	projectPath, err := config.ResolveProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	effective, err := config.ResolveEffective(projectPath, userConfigPath())
	if err != nil {
		return nil, err
	}

	findings := []validation.Finding{
		effectiveStringFinding("agents.runtime", effective.Runtime),
		effectiveStringFinding("runtime_auth.provider", effective.Provider),
		effectiveStringFinding("runtime_auth.mode", effective.AuthMode),
		effectiveStringFinding("runtime_auth.method", effective.AuthMethod),
		effectiveStringFinding("runtime_auth.credential_ref", effective.CredentialRef),
		effectiveStringFinding("models.planner", effective.PlannerModel),
		effectiveStringFinding("models.agent", effective.AgentModel),
		effectiveStringFinding("models.small_tasks", effective.SmallTaskModel),
		effectiveStringFinding("sandbox.provider", effective.SandboxProvider),
		effectiveStringFinding("blueprint", effective.Blueprint),
		effectiveStringSliceFinding("quality_gates", effective.QualityGates),
	}
	return findings, nil
}

func effectiveStringFinding(field string, value config.EffectiveString) validation.Finding {
	if !value.Set {
		return validation.Finding{
			Status:   validation.StatusFail,
			Source:   string(config.ValueSourceMissing),
			Evidence: field + " is not set by global setup or project config",
			Fix:      "run tack setup or set an explicit project override with tack init",
		}
	}
	return validation.Finding{
		Status:   validation.StatusPass,
		Source:   string(value.Source),
		Evidence: fmt.Sprintf("%s=%s", field, value.Value),
	}
}

func effectiveStringSliceFinding(field string, value config.EffectiveStringSlice) validation.Finding {
	if !value.Set || len(value.Value) == 0 {
		return validation.Finding{
			Status:   validation.StatusFail,
			Source:   string(config.ValueSourceMissing),
			Evidence: field + " is not set by global setup or project config",
			Fix:      "run tack setup or set an explicit project override with tack init",
		}
	}
	return validation.Finding{
		Status:   validation.StatusPass,
		Source:   string(value.Source),
		Evidence: fmt.Sprintf("%s=%s", field, strings.Join(value.Value, "; ")),
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

func checkGlobalSetup(ctx context.Context) ([]validation.Finding, error) {
	_ = ctx
	path := userConfigPath()
	state, _, err := loadSetupConfigFile(path)
	if err != nil {
		return nil, err
	}
	if state.Setup.Complete {
		return []validation.Finding{{Status: validation.StatusPass, Source: "global", Evidence: "setup.complete=true"}}, nil
	}
	evidence := "setup.complete=false"
	if phase := firstIncompleteSetupPhase(state); phase != "" {
		evidence += " next_phase=" + phase
	}
	return []validation.Finding{{
		Status:   validation.StatusFail,
		Source:   "global",
		Evidence: evidence,
		Fix:      "run tack setup to completion",
	}}, nil
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
