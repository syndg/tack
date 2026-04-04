package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/runtimeauth"
	"gopkg.in/yaml.v3"
)

func init() {
	rootCmd.AddCommand(initCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a Tack project",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	tackDir := filepath.Join(cwd, ".tack")
	configPath := filepath.Join(tackDir, "config.yaml")

	if _, err := os.Stat(configPath); err == nil {
		var overwrite bool
		err := huh.NewConfirm().
			Title(".tack/config.yaml already exists. Overwrite?").
			Value(&overwrite).
			Run()
		if err != nil {
			return err
		}
		if !overwrite {
			fmt.Println("Aborted.")
			return nil
		}
	}

	store, err := loadCredentialsStore()
	if err != nil {
		return err
	}
	wizard, err := runInitWizard(cmd.Context(), store)
	if err != nil {
		return err
	}

	if !store.HasGit() {
		var addGit bool
		err = huh.NewConfirm().
			Title("Add a GitHub/GitLab token? (needed for PRs, clone, push)").
			Value(&addGit).
			Run()
		if err != nil {
			return err
		}
		if addGit {
			if err := promptGitCredential(store); err != nil {
				return err
			}
		}
	} else {
		fmt.Printf("Using existing git credential from credentials store.\n")
	}

	if wizard.SandboxProvider == "daytona" && !store.HasSandbox("daytona") {
		var daytonaKey string
		err = huh.NewInput().
			Title("Daytona API key").
			EchoMode(huh.EchoModePassword).
			Value(&daytonaKey).
			Run()
		if err != nil {
			return err
		}
		store.SetSandbox("daytona", credentials.SandboxCredential{
			Type:   credentials.TypeAPIKey,
			APIKey: daytonaKey,
		})
	}

	projectCfg := buildProjectConfig(wizard)

	// --- Write .tack/config.yaml ---
	if err := os.MkdirAll(tackDir, 0o755); err != nil {
		return fmt.Errorf("creating .tack directory: %w", err)
	}

	cfgYAML, err := yaml.Marshal(projectCfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(configPath, cfgYAML, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := ensureProjectGitignore(cwd); err != nil {
		return err
	}

	// --- Save credentials ---
	if err := store.Save(); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}
	projectUUID, err := ensureStableProjectID(cwd)
	if err != nil {
		return err
	}
	if daemonClient, err := newDaemonClient(cmd, false); err == nil {
		if _, err := daemonClient.RegisterProject(cmd.Context(), client.ProjectRegistration{
			ProjectID:  projectUUID,
			RootPath:   cwd,
			ConfigPath: configPath,
		}); err != nil {
			return fmt.Errorf("registering project with daemon: %w", err)
		}
	}

	fmt.Printf("\nCreated %s\n", configPath)
	fmt.Printf("Project ID: %s\n", projectUUID)
	fmt.Printf("Credentials saved to %s\n", store.Path())
	return nil
}

var tackGitignoreBlock = []string{
	"# Tack local runtime artifacts",
	"/.tack/project-id",
	"/.tack/*.db",
	"/.tack/*.db-wal",
	"/.tack/*.db-shm",
	"/.tack/data/",
}

func ensureProjectGitignore(root string) error {
	gitignorePath := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading .gitignore: %w", err)
	}
	text := string(content)
	missing := make([]string, 0, len(tackGitignoreBlock))
	for _, line := range tackGitignoreBlock {
		if !strings.Contains(text, line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	text += strings.Join(missing, "\n") + "\n"
	if err := os.WriteFile(gitignorePath, []byte(text), 0o644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}
	return nil
}

type initWizardResult struct {
	Profile         string
	Runtime         string
	SandboxProvider string
	Provider        string
	RuntimeAuthMode string
	CredentialRef   string
	AgentModel      string
	PlannerModel    string
	SmallTaskModel  string
	PostCreate      string
}

func runInitWizard(ctx context.Context, store *credentials.Store) (initWizardResult, error) {
	var result initWizardResult
	if err := huh.NewSelect[string]().
		Title("Setup mode").
		Options(
			huh.NewOption("Quick", "quick"),
			huh.NewOption("Advanced", "advanced"),
		).
		Value(&result.Profile).
		Run(); err != nil {
		return result, err
	}
	if err := huh.NewSelect[string]().
		Title("Runtime").
		Options(
			huh.NewOption("Pi", "pi"),
			huh.NewOption("Claude Code", "claude-code"),
		).
		Value(&result.Runtime).
		Run(); err != nil {
		return result, err
	}
	if err := huh.NewSelect[string]().
		Title("Sandbox provider").
		Options(
			huh.NewOption("Local (git worktrees)", "local"),
			huh.NewOption("Daytona (cloud sandboxes)", "daytona"),
		).
		Value(&result.SandboxProvider).
		Run(); err != nil {
		return result, err
	}

	adapter, err := runtimeauth.New(result.Runtime)
	if err != nil {
		return result, err
	}
	probe, err := adapter.Probe(ctx)
	if err != nil {
		return result, err
	}
	if result.SandboxProvider == "local" && !probe.RuntimeAvailable {
		return result, fmt.Errorf("%s is not installed locally; use Daytona or install the runtime first", result.Runtime)
	}

	provider, err := selectInitProvider(adapter, probe, result.Runtime)
	if err != nil {
		return result, err
	}
	result.Provider = provider
	result.CredentialRef = runtimeauth.CredentialRefForProvider(provider)

	if probe.NativeAvailable {
		fmt.Printf("\nAuth detection: %s\n", probe.NativeDescription)
	} else {
		fmt.Printf("\nAuth detection: no native %s auth found\n", result.Runtime)
	}

	nativeEligible := result.SandboxProvider == "local" && slices.Contains(probe.NativeProviders, result.Provider)
	if nativeEligible {
		if err := huh.NewSelect[string]().
			Title("Authentication source").
			Options(
				huh.NewOption("Reference detected native auth", runtimeauth.ModeNative),
				huh.NewOption("Use Tack-managed credential", runtimeauth.ModeTack),
			).
			Value(&result.RuntimeAuthMode).
			Run(); err != nil {
			return result, err
		}
	} else {
		result.RuntimeAuthMode = runtimeauth.ModeTack
		if result.SandboxProvider != "local" && slices.Contains(probe.NativeProviders, result.Provider) {
			fmt.Printf("Native %s auth cannot be projected to %s sandboxes yet; Tack will use a managed credential instead.\n", result.Runtime, result.SandboxProvider)
		}
	}

	if result.RuntimeAuthMode == runtimeauth.ModeTack {
		if err := ensureWizardProviderCredential(store, result.CredentialRef, result.Provider); err != nil {
			return result, err
		}
	}

	models := adapter.Models(result.Provider)
	if len(models) == 0 {
		return result, fmt.Errorf("no models available for provider %s on runtime %s", result.Provider, result.Runtime)
	}
	if err := promptModelSelections(&result, models); err != nil {
		return result, err
	}
	if err := huh.NewInput().
		Title("Optional project post-create commands? (e.g., bun install; Tack runtime bootstrap is automatic)").
		Value(&result.PostCreate).
		Run(); err != nil {
		return result, err
	}
	if err := confirmInitPlan(result, probe); err != nil {
		return result, err
	}
	return result, nil
}

func confirmInitPlan(result initWizardResult, probe runtimeauth.ProbeResult) error {
	summary := []string{
		fmt.Sprintf("Runtime: %s", result.Runtime),
		fmt.Sprintf("Sandbox: %s", result.SandboxProvider),
		fmt.Sprintf("Provider: %s", result.Provider),
		fmt.Sprintf("Auth mode: %s", result.RuntimeAuthMode),
		fmt.Sprintf("Agent model: %s", result.AgentModel),
		fmt.Sprintf("Planner model: %s", result.PlannerModel),
		fmt.Sprintf("Small-task model: %s", result.SmallTaskModel),
	}
	if result.RuntimeAuthMode == runtimeauth.ModeTack {
		summary = append(summary, fmt.Sprintf("Credential ref: %s", result.CredentialRef))
	}
	if probe.NativeDescription != "" {
		summary = append(summary, fmt.Sprintf("Detected auth: %s", probe.NativeDescription))
	}
	if result.PostCreate != "" {
		summary = append(summary, fmt.Sprintf("Post-create: %s", result.PostCreate))
	}
	var proceed bool
	if err := huh.NewConfirm().
		Title("Write this Tack config?\n\n" + strings.Join(summary, "\n")).
		Value(&proceed).
		Run(); err != nil {
		return err
	}
	if !proceed {
		return fmt.Errorf("init cancelled")
	}
	return nil
}

func selectInitProvider(adapter runtimeauth.Adapter, probe runtimeauth.ProbeResult, runtimeName string) (string, error) {
	if runtimeName == "claude-code" {
		return "anthropic", nil
	}
	options := make([]huh.Option[string], 0, len(probe.SupportedProviders))
	for _, provider := range probe.SupportedProviders {
		label := provider.Label
		if slices.Contains(probe.NativeProviders, provider.ID) {
			label += " (native auth detected)"
		}
		options = append(options, huh.NewOption(label, provider.ID))
	}
	if len(options) == 0 {
		return "", fmt.Errorf("no providers available for runtime %s", adapter.Runtime())
	}
	var provider string
	if err := huh.NewSelect[string]().
		Title("Provider").
		Options(options...).
		Value(&provider).
		Run(); err != nil {
		return "", err
	}
	return provider, nil
}

func promptModelSelections(result *initWizardResult, models []runtimeauth.ModelOption) error {
	options := make([]huh.Option[string], 0, len(models))
	for _, model := range models {
		options = append(options, huh.NewOption(model.Label, model.ID))
	}
	if err := huh.NewSelect[string]().
		Title("Agent model").
		Options(options...).
		Value(&result.AgentModel).
		Run(); err != nil {
		return err
	}
	if result.Profile == "advanced" {
		plannerOptions := append([]huh.Option[string]{huh.NewOption("Same as agent", result.AgentModel)}, options...)
		if err := huh.NewSelect[string]().
			Title("Planner model").
			Options(plannerOptions...).
			Value(&result.PlannerModel).
			Run(); err != nil {
			return err
		}
		if result.PlannerModel == "" {
			result.PlannerModel = result.AgentModel
		}
	} else {
		result.PlannerModel = result.AgentModel
	}
	smallTaskOptions := append([]huh.Option[string]{huh.NewOption("Same as agent", result.AgentModel)}, options...)
	if err := huh.NewSelect[string]().
		Title("Small-task model").
		Options(smallTaskOptions...).
		Value(&result.SmallTaskModel).
		Run(); err != nil {
		return err
	}
	if result.SmallTaskModel == "" {
		result.SmallTaskModel = result.AgentModel
	}
	return nil
}

func ensureWizardProviderCredential(store *credentials.Store, credentialRef, displayProvider string) error {
	if store.HasProvider(credentialRef) {
		var useExisting bool
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("Use existing Tack credential for %s?", displayProvider)).
			Value(&useExisting).
			Run(); err != nil {
			return err
		}
		if useExisting {
			return nil
		}
	}
	return promptModelCredential(store, credentialRef, displayProvider)
}

func buildProjectConfig(result initWizardResult) map[string]interface{} {
	projectCfg := map[string]interface{}{
		"sandbox": map[string]interface{}{
			"provider": result.SandboxProvider,
		},
		"agents": map[string]interface{}{
			"runtime": result.Runtime,
		},
		"runtime_auth": map[string]interface{}{
			"mode":     result.RuntimeAuthMode,
			"runtime":  result.Runtime,
			"provider": result.Provider,
		},
		"models": map[string]interface{}{
			"default":     result.AgentModel,
			"agent":       result.AgentModel,
			"planner":     result.PlannerModel,
			"small_tasks": result.SmallTaskModel,
		},
	}
	if result.Runtime == "pi" {
		agentsMap := projectCfg["agents"].(map[string]interface{})
		agentsMap["pi"] = map[string]interface{}{"provider": result.Provider}
	}
	if result.RuntimeAuthMode == runtimeauth.ModeTack {
		authMap := projectCfg["runtime_auth"].(map[string]interface{})
		authMap["credential_ref"] = result.CredentialRef
	}
	if result.PostCreate != "" {
		sandboxMap := projectCfg["sandbox"].(map[string]interface{})
		sandboxMap["post_create"] = []string{result.PostCreate}
	}
	return projectCfg
}

func promptModelCredential(store *credentials.Store, credentialRef, displayProvider string) error {
	var credType string

	if credentialRef == "anthropic" {
		err := huh.NewSelect[string]().
			Title("Auth method for "+displayProvider).
			Options(
				huh.NewOption("API Key (usage-based)", credentials.TypeAPIKey),
				huh.NewOption("Setup Token (subscription — run `claude setup-token`)", credentials.TypeSetupToken),
			).
			Value(&credType).
			Run()
		if err != nil {
			return err
		}
	} else {
		credType = credentials.TypeAPIKey
	}

	var value string
	switch credType {
	case credentials.TypeAPIKey:
		err := huh.NewInput().
			Title(fmt.Sprintf("API key for %s", displayProvider)).
			EchoMode(huh.EchoModePassword).
			Value(&value).
			Run()
		if err != nil {
			return err
		}
		store.SetModelProvider(credentialRef, credentials.ProviderCredential{
			Type:   credentials.TypeAPIKey,
			APIKey: value,
		})

	case credentials.TypeSetupToken:
		err := huh.NewInput().
			Title("Paste the setup token from `claude setup-token`").
			EchoMode(huh.EchoModePassword).
			Value(&value).
			Run()
		if err != nil {
			return err
		}
		if err := credentials.ValidateSetupToken(value); err != nil {
			return fmt.Errorf("invalid setup token: %w", err)
		}
		store.SetModelProvider(credentialRef, credentials.ProviderCredential{
			Type:  credentials.TypeSetupToken,
			Token: value,
		})
	}

	return nil
}

func promptGitCredential(store *credentials.Store) error {
	var host string
	err := huh.NewSelect[string]().
		Title("Git host").
		Options(
			huh.NewOption("GitHub (github.com)", "github.com"),
			huh.NewOption("GitLab (gitlab.com)", "gitlab.com"),
			huh.NewOption("Other", "other"),
		).
		Value(&host).
		Run()
	if err != nil {
		return err
	}

	if host == "other" {
		err = huh.NewInput().
			Title("Git host URL").
			Value(&host).
			Run()
		if err != nil {
			return err
		}
	}

	var token string
	err = huh.NewInput().
		Title("Personal access token for " + host).
		EchoMode(huh.EchoModePassword).
		Value(&token).
		Run()
	if err != nil {
		return err
	}

	store.SetGit(credentials.GitCredential{
		Type:  credentials.TypePAT,
		Host:  host,
		Token: token,
	})
	return nil
}

// ensureTackConfig is a no-arg helper that checks .tack/ exists in the current project root.
func ensureTackConfig() string {
	path, err := config.ResolveProjectConfig("")
	if err != nil {
		return ""
	}
	return path
}
