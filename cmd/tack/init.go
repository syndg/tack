package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
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
	// Check if already initialized
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

	// Load existing credentials store
	store, err := loadCredentialsStore()
	if err != nil {
		return err
	}

	// --- Runtime ---
	var runtime string
	err = huh.NewSelect[string]().
		Title("Which runtime?").
		Options(
			huh.NewOption("Pi (Anthropic's agentic runtime)", "pi"),
			huh.NewOption("Claude Code", "claude-code"),
		).
		Value(&runtime).
		Run()
	if err != nil {
		return err
	}

	// --- Model Provider ---
	var provider string
	err = huh.NewSelect[string]().
		Title("Which model provider?").
		Options(
			huh.NewOption("Anthropic", "anthropic"),
			huh.NewOption("OpenAI", "openai"),
			huh.NewOption("Gemini", "gemini"),
			huh.NewOption("Groq", "groq"),
			huh.NewOption("Mistral", "mistral"),
			huh.NewOption("xAI", "xai"),
		).
		Value(&provider).
		Run()
	if err != nil {
		return err
	}

	// --- Model Provider Credential (skip if already stored) ---
	if !store.HasProvider(provider) {
		if err := promptModelCredential(store, provider); err != nil {
			return err
		}
	} else {
		fmt.Printf("Using existing %s credential from credentials store.\n", provider)
	}

	// --- Git Token ---
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

	// --- Sandbox Provider ---
	var sandboxProvider string
	err = huh.NewSelect[string]().
		Title("Sandbox provider?").
		Options(
			huh.NewOption("Local (git worktrees)", "local"),
			huh.NewOption("Daytona (cloud sandboxes)", "daytona"),
		).
		Value(&sandboxProvider).
		Run()
	if err != nil {
		return err
	}

	// --- Daytona API Key ---
	if sandboxProvider == "daytona" && !store.HasSandbox("daytona") {
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

	// --- Post-Create Commands ---
	var postCreate string
	err = huh.NewInput().
		Title("Post-create commands? (e.g., bun install, npm install — leave empty to skip)").
		Value(&postCreate).
		Run()
	if err != nil {
		return err
	}

	// --- Build config ---
	projectCfg := map[string]interface{}{
		"sandbox": map[string]interface{}{
			"provider": sandboxProvider,
		},
		"agents": map[string]interface{}{
			"runtime": runtime,
			"pi": map[string]interface{}{
				"provider": provider,
			},
		},
	}

	if postCreate != "" {
		sandboxMap := projectCfg["sandbox"].(map[string]interface{})
		sandboxMap["post_create"] = []string{postCreate}
	}

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

	// --- Save credentials ---
	if err := store.Save(); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Printf("\nCreated %s\n", configPath)
	fmt.Printf("Credentials saved to %s\n", store.Path())
	return nil
}

func promptModelCredential(store *credentials.Store, provider string) error {
	var credType string

	if provider == "anthropic" {
		err := huh.NewSelect[string]().
			Title("Auth method for "+provider).
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
			Title(fmt.Sprintf("API key for %s", provider)).
			EchoMode(huh.EchoModePassword).
			Value(&value).
			Run()
		if err != nil {
			return err
		}
		store.SetModelProvider(provider, credentials.ProviderCredential{
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
		store.SetModelProvider(provider, credentials.ProviderCredential{
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
		Title("Personal access token for "+host).
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
	return config.ResolveProjectConfig("")
}
