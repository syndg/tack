package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/blueprintconfig"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/runtimecatalog"
	"gopkg.in/yaml.v3"
)

var (
	setupNonInteractive     bool
	setupDaemonListen       string
	setupDaemonService      string
	setupRuntime            string
	setupProvider           string
	setupAuthMode           string
	setupAuthMethod         string
	setupCredentialRef      string
	setupAPIKey             string
	setupOAuthAccessToken   string
	setupOAuthRefreshToken  string
	setupOAuthExpiresAt     string
	setupAgentModel         string
	setupPlannerModel       string
	setupSmallTaskModel     string
	setupSandboxProvider    string
	setupBlueprint          string
	setupQualityGates       []string
	setupQualityGatesInput  string
	setupGitHubToken        string
	setupInstallPi          bool
	setupRuntimeRunner      runtimecatalog.Runner = runtimecatalog.ExecRunner{}
	setupDaemonCanSeePiFunc                       = runtimecatalog.DaemonCanSeePi
)

var setupPhaseOrder = []string{"daemon_service", "runtime", "provider_auth", "models", "blueprint", "github_pr", "final_validation"}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure global Tack setup",
	RunE:  runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupNonInteractive, "non-interactive", false, "require setup inputs from flags and fail if any are missing")
	setupCmd.Flags().StringVar(&setupDaemonListen, "daemon-listen", "", "daemon listen address to save globally")
	setupCmd.Flags().StringVar(&setupDaemonService, "daemon-service", "", "daemon service mode: service or foreground")
	setupCmd.Flags().StringVar(&setupRuntime, "runtime", "", "agent runtime to save globally")
	setupCmd.Flags().StringVar(&setupProvider, "provider", "", "model provider to save globally")
	setupCmd.Flags().StringVar(&setupAuthMode, "auth-mode", "", "runtime auth mode: native or tack")
	setupCmd.Flags().StringVar(&setupAuthMethod, "auth-method", "", "credential method: api_key or oauth")
	setupCmd.Flags().StringVar(&setupCredentialRef, "credential-ref", "", "credential reference to bind globally")
	setupCmd.Flags().StringVar(&setupAPIKey, "api-key", "", "API key value for provider credential setup")
	setupCmd.Flags().StringVar(&setupOAuthAccessToken, "oauth-access-token", "", "OAuth access token for non-interactive credential setup")
	setupCmd.Flags().StringVar(&setupOAuthRefreshToken, "oauth-refresh-token", "", "OAuth refresh token for non-interactive credential setup")
	setupCmd.Flags().StringVar(&setupOAuthExpiresAt, "oauth-expires-at", "", "OAuth expiry as Unix milliseconds for non-interactive credential setup")
	setupCmd.Flags().StringVar(&setupAgentModel, "agent-model", "", "agent model to save globally")
	setupCmd.Flags().StringVar(&setupPlannerModel, "planner-model", "", "planner model to save globally")
	setupCmd.Flags().StringVar(&setupSmallTaskModel, "small-task-model", "", "small-task model to save globally")
	setupCmd.Flags().StringVar(&setupSandboxProvider, "sandbox-provider", "", "sandbox provider to save globally")
	setupCmd.Flags().StringVar(&setupBlueprint, "blueprint", "", "global blueprint id")
	setupCmd.Flags().StringArrayVar(&setupQualityGates, "quality-gate", nil, "quality gate command to save globally; repeat for multiple gates")
	setupCmd.Flags().StringVar(&setupGitHubToken, "github-token", "", "GitHub token for PR workflows")
	setupCmd.Flags().BoolVar(&setupInstallPi, "install-pi", false, "install Pi when selected and missing")
	rootCmd.AddCommand(setupCmd)
}

type setupConfigFile struct {
	Setup        config.SetupConfig       `yaml:"setup"`
	Daemon       config.DaemonConfig      `yaml:"daemon,omitempty"`
	Agents       config.AgentsConfig      `yaml:"agents,omitempty"`
	RuntimeAuth  config.RuntimeAuthConfig `yaml:"runtime_auth,omitempty"`
	Models       config.ModelsConfig      `yaml:"models,omitempty"`
	Sandbox      config.SandboxConfig     `yaml:"sandbox,omitempty"`
	Blueprint    string                   `yaml:"blueprint,omitempty"`
	QualityGates []string                 `yaml:"quality_gates,omitempty"`
}

func runSetup(cmd *cobra.Command, args []string) error {
	path := userConfigPath()
	state, stat, err := loadSetupConfigFile(path)
	if err != nil {
		return err
	}
	if state.Setup.Phases == nil {
		state.Setup.Phases = map[string]config.SetupPhase{}
	}
	firstPhase := firstSetupPhaseToRun(state)
	if firstPhase == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Global setup is complete")
		return nil
	}

	if setupNonInteractive {
		if err := missingSetupInputs(state); len(err) > 0 {
			return fmt.Errorf("non-interactive setup requires %s", strings.Join(err, ", "))
		}
	}

	for _, phase := range setupPhaseOrder[startSetupIndex(firstPhase):] {
		if phase == "final_validation" {
			if err := validateSetupState(cmd.Context(), state); err != nil {
				state.Setup.Phases["final_validation"] = config.SetupPhase{}
				state.Setup.Complete = false
				_ = writeSetupConfigFileAtomic(path, state, stat)
				return err
			}
			if err := saveSetupCredentials(state); err != nil {
				return err
			}
			state.Setup.Complete = true
			if err := persistSetupPhase(path, &state, &stat, phase); err != nil {
				return err
			}
			break
		}
		if setupNonInteractive {
			if err := applyNonInteractiveSetupPhase(cmd.Context(), &state, phase); err != nil {
				return err
			}
		} else if err := applyInteractiveSetupPhase(&state, phase); err != nil {
			return err
		}
		if err := saveSetupCredentials(state); err != nil {
			return err
		}
		if err := persistSetupPhase(path, &state, &stat, phase); err != nil {
			return err
		}
	}
	renderSetupSummary(cmd, state)
	return nil
}

func applyNonInteractiveSetupPhase(ctx context.Context, state *setupConfigFile, phase string) error {
	switch phase {
	case "daemon_service":
		state.Daemon.Listen = setupDaemonListen
		state.Setup.Service = setupDaemonService
	case "runtime":
		state.Agents.Runtime = setupRuntime
		if setupRuntime == "pi" {
			plan, err := runtimecatalog.PlanPiInstall(ctx, setupRuntimeRunner, setupInstallPi)
			if err != nil {
				return err
			}
			if plan.RequiresConsent {
				return fmt.Errorf("Pi installation requires explicit consent with --install-pi")
			}
		}
	case "provider_auth":
		state.RuntimeAuth.Runtime = setupRuntime
		state.RuntimeAuth.Provider = setupProvider
		state.RuntimeAuth.Mode = setupAuthMode
		state.RuntimeAuth.Method = setupAuthMethod
		state.RuntimeAuth.CredentialRef = setupCredentialRef
	case "models":
		state.Models.Agent = setupAgentModel
		state.Models.Planner = setupPlannerModel
		state.Models.SmallTasks = setupSmallTaskModel
	case "blueprint":
		state.Sandbox.Provider = setupSandboxProvider
		state.Blueprint = setupBlueprint
		state.QualityGates = append([]string(nil), setupQualityGates...)
	case "github_pr":
		// Credentials are persisted by saveSetupCredentials after the phase succeeds.
	}
	return nil
}

func applyInteractiveSetupPhase(state *setupConfigFile, phase string) error {
	switch phase {
	case "daemon_service":
		if err := huh.NewSelect[string]().Title("Daemon service mode").Options(huh.NewOption("Service", "service"), huh.NewOption("Foreground", "foreground")).Value(&setupDaemonService).Run(); err != nil {
			return err
		}
		if state.Daemon.Listen != "" {
			fmt.Printf("Existing daemon.listen: %s\n", state.Daemon.Listen)
		}
		if err := huh.NewInput().Title("Daemon listen address").Value(&setupDaemonListen).Run(); err != nil {
			return err
		}
		state.Setup.Service = setupDaemonService
		state.Daemon.Listen = setupDaemonListen
	case "runtime":
		if state.Agents.Runtime != "" {
			fmt.Printf("Existing runtime: %s\n", state.Agents.Runtime)
		}
		if err := huh.NewSelect[string]().Title("Runtime").Options(huh.NewOption("Pi", "pi"), huh.NewOption("Claude Code", "claude-code")).Value(&setupRuntime).Run(); err != nil {
			return err
		}
		state.Agents.Runtime = setupRuntime
	case "provider_auth":
		if err := huh.NewInput().Title("Provider").Value(&setupProvider).Run(); err != nil {
			return err
		}
		if err := huh.NewSelect[string]().Title("Auth mode").Options(huh.NewOption("Native", "native"), huh.NewOption("Tack credential", "tack")).Value(&setupAuthMode).Run(); err != nil {
			return err
		}
		if setupAuthMode == "tack" {
			if err := huh.NewSelect[string]().Title("Credential method").Options(huh.NewOption("API key", "api_key"), huh.NewOption("OAuth", "oauth")).Value(&setupAuthMethod).Run(); err != nil {
				return err
			}
			if err := huh.NewInput().Title("Credential reference").Value(&setupCredentialRef).Run(); err != nil {
				return err
			}
		}
		state.RuntimeAuth = config.RuntimeAuthConfig{Runtime: state.Agents.Runtime, Provider: setupProvider, Mode: setupAuthMode, Method: setupAuthMethod, CredentialRef: setupCredentialRef}
	case "models":
		if err := huh.NewInput().Title("Planner model").Value(&setupPlannerModel).Run(); err != nil {
			return err
		}
		if err := huh.NewInput().Title("Agent model").Value(&setupAgentModel).Run(); err != nil {
			return err
		}
		if err := huh.NewInput().Title("Small-task model").Value(&setupSmallTaskModel).Run(); err != nil {
			return err
		}
		state.Models = config.ModelsConfig{Planner: setupPlannerModel, Agent: setupAgentModel, SmallTasks: setupSmallTaskModel}
	case "blueprint":
		if err := huh.NewInput().Title("Sandbox provider").Value(&setupSandboxProvider).Run(); err != nil {
			return err
		}
		if err := huh.NewInput().Title("Blueprint").Value(&setupBlueprint).Run(); err != nil {
			return err
		}
		if err := huh.NewInput().Title("Quality gate command").Value(&setupQualityGatesInput).Run(); err != nil {
			return err
		}
		state.Sandbox.Provider = setupSandboxProvider
		state.Blueprint = setupBlueprint
		state.QualityGates = []string{setupQualityGatesInput}
	case "github_pr":
		if selectedBlueprintNeedsGitHub(state.Blueprint) && setupGitHubToken == "" {
			store, err := loadCredentialsStore()
			if err != nil || !store.HasGit() {
				if err := huh.NewInput().Title("GitHub token").Value(&setupGitHubToken).Run(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func missingSetupInputs(state setupConfigFile) []string {
	var missing []string
	require := func(flag, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, flag)
		}
	}
	require("--daemon-service", setupDaemonService)
	require("--daemon-listen", setupDaemonListen)
	require("--runtime", setupRuntime)
	require("--provider", setupProvider)
	require("--auth-mode", setupAuthMode)
	if setupAuthMode == "tack" {
		require("--auth-method", setupAuthMethod)
		require("--credential-ref", setupCredentialRef)
		if setupAuthMethod == "api_key" {
			require("--api-key", setupAPIKey)
		}
		if setupAuthMethod == "oauth" {
			require("--oauth-access-token", setupOAuthAccessToken)
			require("--oauth-refresh-token", setupOAuthRefreshToken)
			require("--oauth-expires-at", setupOAuthExpiresAt)
		}
	}
	require("--planner-model", setupPlannerModel)
	require("--agent-model", setupAgentModel)
	require("--small-task-model", setupSmallTaskModel)
	require("--sandbox-provider", setupSandboxProvider)
	require("--blueprint", setupBlueprint)
	if len(setupQualityGates) == 0 {
		missing = append(missing, "--quality-gate")
	}
	if selectedBlueprintNeedsGitHub(setupBlueprint) && setupGitHubToken == "" {
		store, err := loadCredentialsStore()
		if err != nil || !store.HasGit() {
			missing = append(missing, "--github-token")
		}
	}
	return missing
}

func selectedBlueprintNeedsGitHub(id string) bool {
	reg, err := blueprintconfig.LoadActiveRegistry(userConfigPath(), "")
	if err != nil {
		return false
	}
	requirements, err := blueprintconfig.ExtractRequirementsFromLookup(registryBlueprintLookup{reg: reg}, id)
	if err != nil {
		return false
	}
	return requirements.CreatePR
}

func validateSetupState(ctx context.Context, state setupConfigFile) error {
	var missing []string
	if state.Setup.Service == "" {
		missing = append(missing, "setup.service")
	}
	if state.Daemon.Listen == "" {
		missing = append(missing, "daemon.listen")
	}
	if state.Agents.Runtime == "" {
		missing = append(missing, "agents.runtime")
	}
	if state.RuntimeAuth.Provider == "" {
		missing = append(missing, "runtime_auth.provider")
	}
	if state.RuntimeAuth.Mode == "" {
		missing = append(missing, "runtime_auth.mode")
	}
	if state.Models.Planner == "" {
		missing = append(missing, "models.planner")
	}
	if state.Models.Agent == "" {
		missing = append(missing, "models.agent")
	}
	if state.Models.SmallTasks == "" {
		missing = append(missing, "models.small_tasks")
	}
	if state.Sandbox.Provider == "" {
		missing = append(missing, "sandbox.provider")
	}
	if state.Blueprint == "" {
		missing = append(missing, "blueprint")
	}
	if len(state.QualityGates) == 0 {
		missing = append(missing, "quality_gates")
	}
	if len(missing) > 0 {
		return fmt.Errorf("setup validation failed: missing %s", strings.Join(missing, ", "))
	}
	if state.Agents.Runtime == "pi" {
		probe := runtimecatalog.ProbePi(ctx, setupRuntimeRunner)
		if !probe.Installed {
			return fmt.Errorf("setup validation failed: pi executable was not found in PATH")
		}
		if probe.CatalogError != "" {
			return fmt.Errorf("setup validation failed: %s", probe.CatalogError)
		}
		if err := validatePiModelSelections(state.RuntimeAuth.Provider, state.Models, probe.Catalog); err != nil {
			return fmt.Errorf("setup validation failed: %w", err)
		}
		if ok, evidence := setupDaemonCanSeePiFunc(ctx, state.Daemon.Listen); !ok {
			return fmt.Errorf("setup validation failed: daemon cannot validate Pi visibility: %s", evidence)
		}
	}
	return nil
}

func validatePiModelSelections(provider string, models config.ModelsConfig, catalog []runtimecatalog.ProviderCatalog) error {
	provider = strings.TrimSpace(provider)
	if !runtimecatalog.CatalogHasProvider(catalog, provider) {
		available := strings.Join(runtimecatalog.CatalogProviderNames(catalog), ", ")
		if available == "" {
			available = "none"
		}
		return fmt.Errorf("provider %q is not in Pi catalog (available providers: %s)", provider, available)
	}
	for role, model := range map[string]string{
		"planner":     models.Planner,
		"agent":       models.Agent,
		"small_tasks": models.SmallTasks,
	} {
		if !runtimecatalog.CatalogHasModel(catalog, provider, model) {
			available := strings.Join(runtimecatalog.CatalogModelIDs(catalog, provider), ", ")
			if available == "" {
				available = "none"
			}
			return fmt.Errorf("%s model %q is not in Pi catalog for provider %q (available models: %s)", role, model, provider, available)
		}
	}
	return nil
}

func persistSetupPhase(path string, state *setupConfigFile, stat *os.FileInfo, phase string) error {
	state.Setup.Phases[phase] = config.SetupPhase{Complete: true}
	if phase != "final_validation" {
		state.Setup.Complete = false
	}
	if err := writeSetupConfigFileAtomic(path, *state, *stat); err != nil {
		return err
	}
	current, err := os.Stat(expandSetupPath(path))
	if err != nil {
		return err
	}
	*stat = current
	return nil
}

func firstSetupPhaseToRun(state setupConfigFile) string {
	if state.Setup.Phases == nil {
		return setupPhaseOrder[0]
	}
	for _, phase := range setupPhaseOrder[:len(setupPhaseOrder)-1] {
		if !state.Setup.Phases[phase].Complete || !setupPhaseValid(state, phase) {
			return phase
		}
	}
	if state.Setup.Complete && state.Setup.Phases["final_validation"].Complete && validateSetupState(context.Background(), state) == nil {
		return ""
	}
	return "final_validation"
}

func setupPhaseValid(state setupConfigFile, phase string) bool {
	switch phase {
	case "daemon_service":
		return state.Setup.Service != "" && state.Daemon.Listen != ""
	case "runtime":
		return state.Agents.Runtime != ""
	case "provider_auth":
		if state.RuntimeAuth.Provider == "" || state.RuntimeAuth.Mode == "" {
			return false
		}
		if state.RuntimeAuth.Mode == "tack" {
			return state.RuntimeAuth.Method != "" && state.RuntimeAuth.CredentialRef != ""
		}
		return true
	case "models":
		return state.Models.Planner != "" && state.Models.Agent != "" && state.Models.SmallTasks != ""
	case "blueprint":
		return state.Sandbox.Provider != "" && state.Blueprint != "" && len(state.QualityGates) > 0
	case "github_pr":
		if !selectedBlueprintNeedsGitHub(state.Blueprint) {
			return true
		}
		store, err := loadCredentialsStore()
		return err == nil && store.HasGit()
	default:
		return false
	}
}

func saveSetupCredentials(state setupConfigFile) error {
	store, err := loadCredentialsStore()
	if err != nil {
		return err
	}
	changed := false
	if state.RuntimeAuth.Mode == "tack" && setupAuthMethod == "api_key" && setupAPIKey != "" {
		store.SetModelProvider(state.RuntimeAuth.Provider, credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: setupAPIKey})
		changed = true
	}
	if state.RuntimeAuth.Mode == "tack" && setupAuthMethod == "oauth" && setupOAuthAccessToken != "" {
		expires, err := strconv.ParseInt(setupOAuthExpiresAt, 10, 64)
		if err != nil {
			return fmt.Errorf("parsing --oauth-expires-at: %w", err)
		}
		store.SetModelProvider(state.RuntimeAuth.Provider, credentials.ProviderCredential{Type: credentials.TypeOAuth, AccessToken: setupOAuthAccessToken, RefreshToken: setupOAuthRefreshToken, ExpiresAt: expires})
		changed = true
	}
	if setupGitHubToken != "" {
		store.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: setupGitHubToken})
		changed = true
	}
	if changed {
		return store.Save()
	}
	return nil
}

func firstIncompleteSetupPhase(state setupConfigFile) string {
	if state.Setup.Complete {
		return ""
	}
	for _, phase := range setupPhaseOrder {
		if !state.Setup.Phases[phase].Complete {
			return phase
		}
	}
	return "final_validation"
}

func startSetupIndex(phase string) int {
	for i, name := range setupPhaseOrder {
		if name == phase {
			return i
		}
	}
	return 0
}

func loadSetupConfigFile(path string) (setupConfigFile, os.FileInfo, error) {
	var state setupConfigFile
	path = expandSetupPath(path)
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil, nil
		}
		return state, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state, nil, err
	}
	if err := yaml.Unmarshal(data, &state); err != nil {
		return state, nil, err
	}
	return state, stat, nil
}

func writeSetupConfigFileAtomic(path string, state setupConfigFile, previous os.FileInfo) error {
	path = expandSetupPath(path)
	if current, err := os.Stat(path); err == nil && previous != nil {
		if current.Size() != previous.Size() || !current.ModTime().Equal(previous.ModTime()) {
			return fmt.Errorf("global config changed during setup; rerun tack setup")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func renderSetupSummary(cmd *cobra.Command, state setupConfigFile) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Global setup saved")
	fmt.Fprintf(out, "setup.service: %s (source: global)\n", state.Setup.Service)
	fmt.Fprintf(out, "daemon.listen: %s (source: global)\n", state.Daemon.Listen)
	fmt.Fprintf(out, "agents.runtime: %s (source: global)\n", state.Agents.Runtime)
	fmt.Fprintf(out, "runtime_auth.provider: %s (source: global)\n", state.RuntimeAuth.Provider)
	fmt.Fprintf(out, "runtime_auth.mode: %s (source: global)\n", state.RuntimeAuth.Mode)
	fmt.Fprintf(out, "runtime_auth.method: %s (source: global)\n", state.RuntimeAuth.Method)
	fmt.Fprintf(out, "runtime_auth.credential_ref: %s (source: global)\n", state.RuntimeAuth.CredentialRef)
	fmt.Fprintf(out, "models.planner: %s (source: global)\n", state.Models.Planner)
	fmt.Fprintf(out, "models.agent: %s (source: global)\n", state.Models.Agent)
	fmt.Fprintf(out, "models.small_tasks: %s (source: global)\n", state.Models.SmallTasks)
	fmt.Fprintf(out, "sandbox.provider: %s (source: global)\n", state.Sandbox.Provider)
	fmt.Fprintf(out, "blueprint: %s (source: global)\n", state.Blueprint)
	fmt.Fprintf(out, "quality_gates: %s (source: global)\n", strings.Join(state.QualityGates, "; "))
}

func expandSetupPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
