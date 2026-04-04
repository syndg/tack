package dispatch

import (
	"log/slog"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/runtimeauth"
)

func injectRuntimeCredentials(creds *credentials.Store, binding config.RuntimeAuthConfig, logger *slog.Logger, envVars map[string]string) {
	if err := runtimeauth.InjectEnv(binding, creds, logger, envVars); err != nil {
		logger.Warn("credential injection failed", "runtime", binding.Runtime, "provider", binding.Provider, "mode", binding.Mode, "error", err)
	}
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
