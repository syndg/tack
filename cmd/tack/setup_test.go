package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/runtimecatalog"
	"gopkg.in/yaml.v3"
)

func TestSetupNonInteractiveWritesCompleteGlobalConfig(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "config.yaml")
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	t.Setenv("HOME", root)
	resetSetupTestState(t)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{
		"setup", "--non-interactive",
		"--daemon-service", "foreground",
		"--daemon-listen", "127.0.0.1:9900",
		"--runtime", "claude-code",
		"--provider", "anthropic",
		"--auth-mode", "native",
		"--planner-model", "planner-model",
		"--agent-model", "agent-model",
		"--small-task-model", "small-model",
		"--sandbox-provider", "local",
		"--blueprint", "custom-no-pr",
		"--quality-gate", "go test ./...",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("setup: %v\nstderr: %s", err, stderr.String())
	}
	var saved setupConfigFile
	data, err := os.ReadFile(userConfig)
	if err != nil {
		t.Fatalf("ReadFile user config: %v", err)
	}
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal saved config: %v\n%s", err, string(data))
	}
	if !saved.Setup.Complete {
		t.Fatalf("setup.complete = false in %#v", saved.Setup)
	}
	for _, phase := range setupPhaseOrder {
		if !saved.Setup.Phases[phase].Complete {
			t.Fatalf("phase %s not complete: %#v", phase, saved.Setup.Phases)
		}
	}
	if saved.Daemon.Listen != "127.0.0.1:9900" || saved.Models.Planner != "planner-model" || saved.Sandbox.Provider != "local" {
		t.Fatalf("saved config = %#v", saved)
	}
	if !strings.Contains(stdout.String(), "models.planner: planner-model (source: global)") {
		t.Fatalf("summary missing source reporting:\n%s", stdout.String())
	}
}

func TestSetupNonInteractiveAggregatesMissingOAuthInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TACK_USER_CONFIG_PATH", filepath.Join(root, "config.yaml"))
	t.Setenv("HOME", root)
	resetSetupTestState(t)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"setup", "--non-interactive",
		"--daemon-service", "foreground",
		"--daemon-listen", "127.0.0.1:9900",
		"--runtime", "claude-code",
		"--provider", "openai-codex",
		"--auth-mode", "tack",
		"--auth-method", "oauth",
		"--credential-ref", "openai-codex",
		"--planner-model", "planner-model",
		"--agent-model", "agent-model",
		"--small-task-model", "small-model",
		"--sandbox-provider", "local",
		"--blueprint", "custom-no-pr",
		"--quality-gate", "go test ./...",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("setup succeeded; want missing OAuth input failure")
	}
	for _, want := range []string{"--oauth-access-token", "--oauth-refresh-token", "--oauth-expires-at"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %s: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("config was written after failed setup: %v", statErr)
	}
}

func TestSetupResumesFromFirstIncompletePhase(t *testing.T) {
	state := setupConfigFile{Setup: config.SetupConfig{Phases: map[string]config.SetupPhase{
		"daemon_service": {Complete: true},
		"runtime":        {Complete: true},
	}}}
	if got := firstIncompleteSetupPhase(state); got != "provider_auth" {
		t.Fatalf("firstIncompleteSetupPhase = %q, want provider_auth", got)
	}
	state.Setup.Complete = true
	if got := firstIncompleteSetupPhase(state); got != "" {
		t.Fatalf("complete setup phase = %q, want empty", got)
	}
}

func TestSetupGitHubTokenOnlyRequiredForPRBlueprint(t *testing.T) {
	resetSetupTestState(t)
	setupDaemonService = "foreground"
	setupDaemonListen = "127.0.0.1:9900"
	setupRuntime = "claude-code"
	setupProvider = "anthropic"
	setupAuthMode = "native"
	setupPlannerModel = "planner-model"
	setupAgentModel = "agent-model"
	setupSmallTaskModel = "small-model"
	setupSandboxProvider = "local"
	setupQualityGates = []string{"go test ./..."}
	setupBlueprint = "custom-no-pr"
	if missing := strings.Join(missingSetupInputs(setupConfigFile{}), ","); strings.Contains(missing, "--github-token") {
		t.Fatalf("custom blueprint missing github token: %s", missing)
	}

	root := t.TempDir()
	t.Setenv("HOME", root)
	setupBlueprint = "standard"
	missing := strings.Join(missingSetupInputs(setupConfigFile{}), ",")
	if !strings.Contains(missing, "--github-token") {
		t.Fatalf("standard blueprint missing inputs = %s, want github token", missing)
	}
}

func TestValidateSetupStateFailsPiModelOutsideCatalog(t *testing.T) {
	resetSetupTestState(t)
	setupRuntimeRunner = fakeDoctorRuntimeRunner{
		paths: map[string]string{"pi": "/tmp/pi"},
		out:   map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\n")},
	}
	state := setupConfigFile{
		Daemon:      config.DaemonConfig{Listen: "127.0.0.1:9900"},
		Agents:      config.AgentsConfig{Runtime: "pi"},
		RuntimeAuth: config.RuntimeAuthConfig{Provider: "anthropic", Mode: "native"},
		Models:      config.ModelsConfig{Planner: "claude-opus-4-1", Agent: "missing-agent", SmallTasks: "claude-opus-4-1"},
		Sandbox:     config.SandboxConfig{Provider: "local"},
		Blueprint:   "custom-no-pr",
		QualityGates: []string{
			"go test ./...",
		},
	}
	err := validateSetupState(context.Background(), state)
	if err == nil || !strings.Contains(err.Error(), "agent model \"missing-agent\" is not in Pi catalog") {
		t.Fatalf("validateSetupState error = %v", err)
	}
}

func TestValidateSetupStateFailsPiProviderOutsideCatalog(t *testing.T) {
	resetSetupTestState(t)
	setupRuntimeRunner = fakeDoctorRuntimeRunner{
		paths: map[string]string{"pi": "/tmp/pi"},
		out:   map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\n")},
	}
	state := setupConfigFile{
		Daemon:       config.DaemonConfig{Listen: "127.0.0.1:9900"},
		Agents:       config.AgentsConfig{Runtime: "pi"},
		RuntimeAuth:  config.RuntimeAuthConfig{Provider: "openai", Mode: "native"},
		Models:       config.ModelsConfig{Planner: "gpt-5", Agent: "gpt-5", SmallTasks: "gpt-5"},
		Sandbox:      config.SandboxConfig{Provider: "local"},
		Blueprint:    "custom-no-pr",
		QualityGates: []string{"go test ./..."},
	}
	err := validateSetupState(context.Background(), state)
	if err == nil || !strings.Contains(err.Error(), "provider \"openai\" is not in Pi catalog") {
		t.Fatalf("validateSetupState error = %v", err)
	}
}

func resetSetupTestState(t *testing.T) {
	t.Helper()
	rootCmd.SetArgs(nil)
	cfgPath = ""
	daemonURL = defaultDaemonURL
	projectID = ""
	setupNonInteractive = false
	setupDaemonListen = ""
	setupDaemonService = ""
	setupRuntime = ""
	setupProvider = ""
	setupAuthMode = ""
	setupAuthMethod = ""
	setupCredentialRef = ""
	setupAPIKey = ""
	setupOAuthAccessToken = ""
	setupOAuthRefreshToken = ""
	setupOAuthExpiresAt = ""
	setupAgentModel = ""
	setupPlannerModel = ""
	setupSmallTaskModel = ""
	setupSandboxProvider = ""
	setupBlueprint = ""
	setupQualityGates = nil
	setupGitHubToken = ""
	setupInstallPi = false
	setupRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"pi": "/tmp/pi"}, out: map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic model 200K 32K yes yes\n")}}
	setupDaemonCanSeePiFunc = func(context.Context, string) (bool, string) { return true, "daemon can see Pi" }
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		cfgPath = ""
		daemonURL = defaultDaemonURL
		projectID = ""
		setupRuntimeRunner = runtimecatalog.ExecRunner{}
		setupDaemonCanSeePiFunc = runtimecatalog.DaemonCanSeePi
	})
}
