package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/providerauth"
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
	AuthMethod      string
	AuthMethodLabel string
	CredentialRef   string
	AgentModel      string
	PlannerModel    string
	SmallTaskModel  string
	SetupCommands   string
	SetupVerify     string
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
	if err := selectInitAuthMethod(ctx, store, &result); err != nil {
		return result, err
	}

	models := adapter.Models(result.Provider)
	if len(models) == 0 {
		return result, fmt.Errorf("no models available for provider %s on runtime %s", result.Provider, result.Runtime)
	}
	if err := promptModelSelections(&result, models); err != nil {
		return result, err
	}
	if err := huh.NewInput().
		Title("Optional project setup commands? (use ';;' between multiple commands, e.g. cd backend && bun install ;; cd frontend && bun install)").
		Value(&result.SetupCommands).
		Run(); err != nil {
		return result, err
	}
	if err := huh.NewInput().
		Title("Optional project setup verify commands? (use ';;' between multiple commands; skipped if empty)").
		Value(&result.SetupVerify).
		Run(); err != nil {
		return result, err
	}
	if err := confirmInitPlan(result); err != nil {
		return result, err
	}
	return result, nil
}

func confirmInitPlan(result initWizardResult) error {
	summary := []string{
		fmt.Sprintf("Runtime: %s", result.Runtime),
		fmt.Sprintf("Sandbox: %s", result.SandboxProvider),
		fmt.Sprintf("Provider: %s", runtimeauth.ProviderDisplayName(result.Provider)),
		fmt.Sprintf("Auth method: %s", result.AuthMethodLabel),
		fmt.Sprintf("Agent model: %s", result.AgentModel),
		fmt.Sprintf("Planner model: %s", result.PlannerModel),
		fmt.Sprintf("Small-task model: %s", result.SmallTaskModel),
	}
	if result.RuntimeAuthMode == runtimeauth.ModeTack {
		summary = append(summary, fmt.Sprintf("Credential ref: %s", result.CredentialRef))
	}
	if result.SetupCommands != "" {
		summary = append(summary, fmt.Sprintf("Project setup commands: %s", result.SetupCommands))
	}
	if result.SetupVerify != "" {
		summary = append(summary, fmt.Sprintf("Project setup verify: %s", result.SetupVerify))
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
		options = append(options, huh.NewOption(provider.Label, provider.ID))
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

func selectInitAuthMethod(ctx context.Context, store *credentials.Store, result *initWizardResult) error {
	providerName := runtimeauth.ProviderDisplayName(result.Provider)

	type authChoice struct {
		Value  string
		Label  string
		Mode   string
		Method string
	}
	var choices []authChoice

	switch result.Provider {
	case "anthropic":
		if store.HasProvider(result.CredentialRef) {
			choices = append(choices, authChoice{Value: "existing-api-key", Label: "Use existing Tack Anthropic API key", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodAPIKey})
		}
		choices = append(choices, authChoice{Value: "new-api-key", Label: "Enter Anthropic API key", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodAPIKey})
	case "openai":
		if store.HasProvider(result.CredentialRef) {
			choices = append(choices, authChoice{Value: "existing-openai-key", Label: "Use existing Tack OpenAI API key", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodAPIKey})
		}
		choices = append(choices, authChoice{Value: "new-openai-key", Label: "Enter OpenAI API key", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodAPIKey})
	case "openai-codex":
		if store.HasProvider(result.CredentialRef) {
			choices = append(choices, authChoice{Value: "existing-codex-auth", Label: "Use existing Tack OpenAI Codex auth", Mode: runtimeauth.ModeTack, Method: existingAuthMethod(store, result.CredentialRef)})
		}
		choices = append(choices,
			authChoice{Value: "new-codex-key", Label: "Enter OpenAI API key", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodAPIKey},
			authChoice{Value: "new-codex-oauth", Label: "Sign in with OpenAI Codex", Mode: runtimeauth.ModeTack, Method: runtimeauth.MethodOAuth},
		)
	}

	if len(choices) == 1 {
		result.RuntimeAuthMode = choices[0].Mode
		result.AuthMethod = choices[0].Method
		result.AuthMethodLabel = choices[0].Label
	} else {
		options := make([]huh.Option[string], 0, len(choices))
		for _, choice := range choices {
			options = append(options, huh.NewOption(choice.Label, choice.Value))
		}
		var picked string
		if err := huh.NewSelect[string]().
			Title(fmt.Sprintf("Auth method for %s", providerName)).
			Options(options...).
			Value(&picked).
			Run(); err != nil {
			return err
		}
		for _, choice := range choices {
			if choice.Value == picked {
				result.RuntimeAuthMode = choice.Mode
				result.AuthMethod = choice.Method
				result.AuthMethodLabel = choice.Label
				break
			}
		}
	}

	switch result.AuthMethodLabel {
	case "Enter Anthropic API key", "Enter OpenAI API key":
		if err := promptAPIKeyCredential(store, result.CredentialRef, providerName); err != nil {
			return err
		}
	case "Sign in with OpenAI Codex":
		if err := promptOpenAICodexOAuth(ctx, store, result.CredentialRef); err != nil {
			return err
		}
	}
	return nil
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
			"method":   result.AuthMethod,
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
	commands := splitSetupCommands(result.SetupCommands)
	verify := splitSetupCommands(result.SetupVerify)
	if len(commands) > 0 || len(verify) > 0 {
		projectCfg["project_setup"] = map[string]interface{}{}
		setupMap := projectCfg["project_setup"].(map[string]interface{})
		if len(commands) > 0 {
			setupMap["commands"] = commands
		}
		if len(verify) > 0 {
			setupMap["verify"] = verify
		}
	}
	return projectCfg
}

func splitSetupCommands(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ";;")
	commands := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			commands = append(commands, trimmed)
		}
	}
	return commands
}

func promptAPIKeyCredential(store *credentials.Store, credentialRef, displayProvider string) error {
	var value string
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

	return nil
}

func promptOpenAICodexOAuth(ctx context.Context, store *credentials.Store, credentialRef string) error {
	creds, err := providerauth.LoginOpenAICodex(ctx, providerauth.OAuthLoginCallbacks{
		OnAuth: func(url, instructions string) {
			fmt.Printf("\nOpenAI Codex sign-in\n%s\n%s\n\n", url, instructions)
		},
		OnPrompt: func(message string) (string, error) {
			var input string
			if err := huh.NewInput().
				Title(message).
				Value(&input).
				Run(); err != nil {
				return "", err
			}
			return input, nil
		},
	})
	if err != nil {
		return err
	}
	store.SetModelProvider(credentialRef, credentials.ProviderCredential{
		Type:         credentials.TypeOAuth,
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		ExpiresAt:    creds.ExpiresAt,
		AccountID:    creds.AccountID,
	})
	return nil
}

func existingAuthMethod(store *credentials.Store, provider string) string {
	cred, err := store.GetProviderCredential(provider)
	if err != nil {
		return runtimeauth.MethodAPIKey
	}
	if cred.Type == credentials.TypeOAuth {
		return runtimeauth.MethodOAuth
	}
	return runtimeauth.MethodAPIKey
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
