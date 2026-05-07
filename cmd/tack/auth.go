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
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/providerauth"
)

func init() {
	authAddCmd.Flags().StringVar(&authAddMethod, "method", "", "credential method: api_key or oauth")
	authAddCmd.Flags().StringVar(&authAddAPIKey, "api-key", "", "API key value for non-interactive credential setup")
	authAddCmd.Flags().StringVar(&authAddOAuthAccessToken, "oauth-access-token", "", "OAuth access token for non-interactive credential setup")
	authAddCmd.Flags().StringVar(&authAddOAuthRefreshToken, "oauth-refresh-token", "", "OAuth refresh token for non-interactive credential setup")
	authAddCmd.Flags().StringVar(&authAddOAuthExpiresAt, "oauth-expires-at", "", "OAuth expiry as Unix milliseconds for non-interactive credential setup")
	authCmd.AddCommand(authAddCmd, authRemoveCmd, authListCmd, authTestCmd)
	rootCmd.AddCommand(authCmd)
}

var (
	authAddMethod            string
	authAddAPIKey            string
	authAddOAuthAccessToken  string
	authAddOAuthRefreshToken string
	authAddOAuthExpiresAt    string
)

func loadCredentialsStore() (*credentials.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	return credentials.Load(filepath.Join(home, ".config", "tack", "credentials.yaml"))
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage credentials",
}

var notifyDaemonAuthChanged = func(ctx context.Context) error {
	provider, err := newDaemonServiceProvider()
	if err != nil {
		return err
	}
	return provider.Reload(ctx)
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
		case "anthropic", "openai", "openai-codex", "gemini", "groq", "mistral", "xai":
			return addModelProvider(store, provider)
		case "github", "gitlab", "git":
			return addGitCredential(store, provider)
		case "daytona":
			return addSandboxCredential(store, provider)
		default:
			return fmt.Errorf("unknown provider %q (supported: anthropic, openai, openai-codex, gemini, groq, mistral, xai, github, gitlab, daytona)", provider)
		}
	},
}

func addModelProvider(store *credentials.Store, provider string) error {
	if authAddAPIKey != "" || authAddMethod != "" || authAddOAuthAccessToken != "" || authAddOAuthRefreshToken != "" || authAddOAuthExpiresAt != "" {
		return addModelProviderNonInteractive(store, provider)
	}
	if provider == "openai-codex" {
		var method string
		err := huh.NewSelect[string]().
			Title("Auth method for OpenAI Codex").
			Options(
				huh.NewOption("API Key", credentials.TypeAPIKey),
				huh.NewOption("Sign in with OpenAI Codex", credentials.TypeOAuth),
			).
			Value(&method).
			Run()
		if err != nil {
			return err
		}
		if method == credentials.TypeOAuth {
			return addOpenAICodexOAuth(store)
		}
	}

	var value string
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

	if err := store.Save(); err != nil {
		return err
	}
	noteAuthReload(context.Background())
	fmt.Printf("Stored %s credential in %s\n", provider, store.Path())
	return nil
}

func addModelProviderNonInteractive(store *credentials.Store, provider string) error {
	method := authAddMethod
	if method == "" && authAddAPIKey != "" {
		method = credentials.TypeAPIKey
	}
	switch method {
	case credentials.TypeAPIKey:
		if authAddAPIKey == "" {
			return fmt.Errorf("non-interactive api_key credential setup requires --api-key")
		}
		store.SetModelProvider(provider, credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: authAddAPIKey})
	case credentials.TypeOAuth:
		if provider != "openai-codex" {
			return fmt.Errorf("oauth credentials are only supported for openai-codex")
		}
		var missing []string
		if authAddOAuthAccessToken == "" {
			missing = append(missing, "--oauth-access-token")
		}
		if authAddOAuthRefreshToken == "" {
			missing = append(missing, "--oauth-refresh-token")
		}
		if authAddOAuthExpiresAt == "" {
			missing = append(missing, "--oauth-expires-at")
		}
		if len(missing) > 0 {
			return fmt.Errorf("non-interactive oauth credential setup requires %s", strings.Join(missing, ", "))
		}
		expiresAt, err := strconv.ParseInt(authAddOAuthExpiresAt, 10, 64)
		if err != nil {
			return fmt.Errorf("parsing --oauth-expires-at: %w", err)
		}
		store.SetModelProvider(provider, credentials.ProviderCredential{Type: credentials.TypeOAuth, AccessToken: authAddOAuthAccessToken, RefreshToken: authAddOAuthRefreshToken, ExpiresAt: expiresAt})
	default:
		return fmt.Errorf("credential method must be api_key or oauth")
	}
	if err := store.Save(); err != nil {
		return err
	}
	noteAuthReload(context.Background())
	fmt.Printf("Stored %s %s credential in %s\n", provider, method, store.Path())
	return nil
}

func addOpenAICodexOAuth(store *credentials.Store) error {
	creds, err := providerauth.LoginOpenAICodex(context.Background(), providerauth.OAuthLoginCallbacks{
		OnAuth: func(url, instructions string) {
			fmt.Printf("\nOpenAI Codex sign-in\n%s\n%s\n\n", url, instructions)
		},
		OnPrompt: func(message string) (string, error) {
			var input string
			if err := huh.NewInput().Title(message).Value(&input).Run(); err != nil {
				return "", err
			}
			return input, nil
		},
	})
	if err != nil {
		return err
	}
	store.SetModelProvider("openai-codex", credentials.ProviderCredential{
		Type:         credentials.TypeOAuth,
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		ExpiresAt:    creds.ExpiresAt,
		AccountID:    creds.AccountID,
	})
	if err := store.Save(); err != nil {
		return err
	}
	noteAuthReload(context.Background())
	fmt.Printf("Stored openai-codex oauth credential in %s\n", store.Path())
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
	noteAuthReload(context.Background())
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
	noteAuthReload(context.Background())
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
		noteAuthReload(cmd.Context())
		fmt.Printf("Removed %s credential\n", provider)
		return nil
	},
}

func noteAuthReload(ctx context.Context) {
	if notifyDaemonAuthChanged == nil {
		return
	}
	if err := notifyDaemonAuthChanged(ctx); err == nil {
		fmt.Println("Triggered daemon reload")
	}
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
