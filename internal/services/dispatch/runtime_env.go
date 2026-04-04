package dispatch

import (
	"log/slog"

	"github.com/syndg/tack/internal/credentials"
)

func injectRuntimeCredentials(creds *credentials.Store, provider string, logger *slog.Logger, envVars map[string]string) {
	if creds == nil {
		return
	}
	if provider == "" {
		return
	}
	resolved, err := creds.ModelProvider(provider)
	if err != nil {
		logger.Warn("credential injection: model provider not found", "provider", provider, "error", err)
		return
	}
	envName, ok := providerEnvVars[provider]
	if !ok {
		logger.Warn("credential injection: no env var mapping for provider", "provider", provider)
		return
	}
	envVars[envName] = resolved.Value
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
