package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/syndg/deck/internal/credentials"
)

func init() {
	authCmd.AddCommand(authAddCmd, authRemoveCmd, authListCmd, authTestCmd)
	rootCmd.AddCommand(authCmd)
}

func loadCredentialsStore() (*credentials.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	return credentials.Load(filepath.Join(home, ".config", "deck", "credentials.yaml"))
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage credentials",
}

var authAddCmd = &cobra.Command{
	Use:   "add <provider>",
	Short: "Add or replace a credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		store, err := loadCredentialsStore()
		if err != nil {
			return err
		}

		switch provider {
		case "anthropic", "openai", "gemini", "groq", "mistral", "xai":
			return addModelProvider(store, provider)
		case "github", "gitlab", "git":
			return addGitCredential(store, provider)
		case "daytona":
			return addSandboxCredential(store, provider)
		default:
			return fmt.Errorf("unknown provider %q (supported: anthropic, openai, gemini, groq, mistral, xai, github, gitlab, daytona)", provider)
		}
	},
}

func addModelProvider(store *credentials.Store, provider string) error {
	var credType string
	typeOptions := []huh.Option[string]{
		huh.NewOption("API Key", credentials.TypeAPIKey),
	}
	if provider == "anthropic" {
		typeOptions = append(typeOptions, huh.NewOption("Setup Token (subscription)", credentials.TypeSetupToken))
	}

	if len(typeOptions) == 1 {
		credType = credentials.TypeAPIKey
	} else {
		err := huh.NewSelect[string]().
			Title("Auth method for " + provider).
			Options(typeOptions...).
			Value(&credType).
			Run()
		if err != nil {
			return err
		}
	}

	var value string
	switch credType {
	case credentials.TypeAPIKey:
		err := huh.NewInput().
			Title("API key for " + provider).
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
			Title("Setup token (run `claude setup-token` to get one)").
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

	if err := store.Save(); err != nil {
		return err
	}
	fmt.Printf("Stored %s credential in %s\n", provider, store.Path())
	return nil
}

func addGitCredential(store *credentials.Store, provider string) error {
	host := "github.com"
	if provider == "gitlab" {
		host = "gitlab.com"
	} else if provider == "git" {
		err := huh.NewInput().
			Title("Git host").
			Value(&host).
			Run()
		if err != nil {
			return err
		}
	}

	var token string
	err := huh.NewInput().
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

	if err := store.Save(); err != nil {
		return err
	}
	fmt.Printf("Stored git credential for %s in %s\n", host, store.Path())
	return nil
}

func addSandboxCredential(store *credentials.Store, provider string) error {
	var key string
	err := huh.NewInput().
		Title("API key for " + provider).
		EchoMode(huh.EchoModePassword).
		Value(&key).
		Run()
	if err != nil {
		return err
	}

	store.SetSandbox(provider, credentials.SandboxCredential{
		Type:   credentials.TypeAPIKey,
		APIKey: key,
	})

	if err := store.Save(); err != nil {
		return err
	}
	fmt.Printf("Stored %s credential in %s\n", provider, store.Path())
	return nil
}

var authRemoveCmd = &cobra.Command{
	Use:   "remove <provider>",
	Short: "Remove a credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		store, err := loadCredentialsStore()
		if err != nil {
			return err
		}

		switch {
		case provider == "github" || provider == "gitlab" || provider == "git":
			store.RemoveGit()
		case provider == "daytona":
			store.RemoveSandbox(provider)
		default:
			store.RemoveModelProvider(provider)
		}

		if err := store.Save(); err != nil {
			return err
		}
		fmt.Printf("Removed %s credential\n", provider)
		return nil
	},
}

var authListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored credentials (values masked)",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := loadCredentialsStore()
		if err != nil {
			return err
		}

		providers := store.ListProviders()
		if len(providers) == 0 && !store.HasGit() {
			fmt.Println("No credentials stored.")
			return nil
		}

		for name, typ := range providers {
			fmt.Printf("  %-15s %s\n", name, typ)
		}
		if store.HasGit() {
			host := store.GitHost()
			fmt.Printf("  %-15s pat (%s)\n", "git", host)
		}

		// Check sandbox credentials
		for _, name := range []string{"daytona"} {
			if store.HasSandbox(name) {
				fmt.Printf("  %-15s api_key\n", name)
			}
		}

		return nil
	},
}

var authTestCmd = &cobra.Command{
	Use:   "test <provider>",
	Short: "Verify a credential works",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		store, err := loadCredentialsStore()
		if err != nil {
			return err
		}

		switch {
		case provider == "github" || provider == "gitlab" || provider == "git":
			tok, err := store.GitToken("")
			if err != nil {
				return fmt.Errorf("resolving git token: %w", err)
			}
			fmt.Printf("Git token resolved (%d chars, %s...)\n", len(tok), mask(tok))
			return nil

		case provider == "daytona":
			key, err := store.SandboxKey("daytona")
			if err != nil {
				return fmt.Errorf("resolving daytona key: %w", err)
			}
			fmt.Printf("Daytona key resolved (%d chars, %s...)\n", len(key), mask(key))
			return nil

		default:
			resolved, err := store.ModelProvider(provider)
			if err != nil {
				return fmt.Errorf("resolving %s credential: %w", provider, err)
			}
			fmt.Printf("%s %s resolved (%d chars, %s...)\n", provider, resolved.Type, len(resolved.Value), mask(resolved.Value))
			return nil
		}
	},
}

func mask(s string) string {
	if len(s) < 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}
