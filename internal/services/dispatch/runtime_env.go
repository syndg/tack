package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/sandbox"
)

const projectedPIAgentDir = ".tack-ext/pi-agent"

type projectedPICredential struct {
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Access  string `json:"access,omitempty"`
	Refresh string `json:"refresh,omitempty"`
	Expires int64  `json:"expires,omitempty"`
	Account string `json:"accountId,omitempty"`
}

func injectRuntimeCredentials(ctx context.Context, runtimeName, provider string, creds *credentials.Store, logger *slog.Logger, sb sandbox.Sandbox, envVars map[string]string) error {
	if creds == nil || provider == "" {
		return nil
	}
	cred, err := creds.GetProviderCredential(provider)
	if err != nil {
		if logger != nil {
			logger.Warn("provider auth unavailable", "runtime", runtimeName, "provider", provider, "error", err)
		}
		return nil
	}
	if runtimeName == "pi" {
		payload := map[string]projectedPICredential{provider: projectPICredential(*cred)}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := sb.Upload(ctx, data, projectedPIAgentDir+"/auth.json"); err != nil {
			return fmt.Errorf("uploading projected Pi auth: %w", err)
		}
		envVars["PI_CODING_AGENT_DIR"] = projectedPIAgentDir
		return nil
	}
	for key, value := range envForProvider(provider, *cred) {
		envVars[key] = value
	}
	return nil
}

func injectRuntimeGitIdentity(name, email string, envVars map[string]string) {
	if name == "" || email == "" {
		return
	}
	envVars["GIT_AUTHOR_NAME"] = name
	envVars["GIT_AUTHOR_EMAIL"] = email
	envVars["GIT_COMMITTER_NAME"] = name
	envVars["GIT_COMMITTER_EMAIL"] = email
}

func projectPICredential(cred credentials.ProviderCredential) projectedPICredential {
	projected := projectedPICredential{Type: cred.Type}
	switch cred.Type {
	case credentials.TypeAPIKey:
		projected.Key = cred.APIKey
	case credentials.TypeOAuth:
		projected.Access = cred.AccessToken
		projected.Refresh = cred.RefreshToken
		projected.Expires = cred.ExpiresAt
		projected.Account = cred.AccountID
	}
	return projected
}

func envForProvider(provider string, cred credentials.ProviderCredential) map[string]string {
	result := map[string]string{}
	switch provider {
	case "anthropic":
		if cred.Type == credentials.TypeOAuth {
			result["ANTHROPIC_OAUTH_TOKEN"] = cred.AccessToken
		} else {
			result["ANTHROPIC_API_KEY"] = cred.APIKey
		}
	case "openai", "openai-codex":
		if cred.Type == credentials.TypeOAuth {
			result["OPENAI_API_KEY"] = cred.AccessToken
		} else {
			result["OPENAI_API_KEY"] = cred.APIKey
		}
	}
	return result
}
