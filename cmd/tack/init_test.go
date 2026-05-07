package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestBuildProjectConfigWritesRuntimeAuthAndModels(t *testing.T) {
	configMap := buildProjectConfig(initWizardResult{
		Runtime:         "pi",
		SandboxProvider: "local",
		Provider:        "openai-codex",
		RuntimeAuthMode: "tack",
		AuthMethod:      "api_key",
		AuthMethodLabel: "Enter OpenAI Codex API key",
		CredentialRef:   "openai-codex",
		AgentModel:      "gpt-5.4",
		PlannerModel:    "gpt-5.4",
		SmallTaskModel:  "gpt-5.4-mini",
		SetupCommands:   "bun install;;bun run typecheck",
		SetupVerify:     "test -d node_modules",
	})
	runtimeAuth := configMap["runtime_auth"].(map[string]interface{})
	if runtimeAuth["provider"] != "openai-codex" {
		t.Fatalf("provider = %v, want openai-codex", runtimeAuth["provider"])
	}
	if runtimeAuth["method"] != "api_key" {
		t.Fatalf("method = %v, want api_key", runtimeAuth["method"])
	}
	if runtimeAuth["credential_ref"] != "openai-codex" {
		t.Fatalf("credential_ref = %v, want openai-codex", runtimeAuth["credential_ref"])
	}
	models := configMap["models"].(map[string]interface{})
	if models["planner"] != "gpt-5.4" || models["small_tasks"] != "gpt-5.4-mini" {
		t.Fatalf("models = %#v", models)
	}
	agents := configMap["agents"].(map[string]interface{})
	piConfig := agents["pi"].(map[string]interface{})
	if piConfig["provider"] != "openai-codex" {
		t.Fatalf("agents.pi.provider = %v, want openai-codex", piConfig["provider"])
	}
	setup := configMap["project_setup"].(map[string]interface{})
	commands := setup["commands"].([]string)
	verify := setup["verify"].([]string)
	if len(commands) != 2 || commands[0] != "bun install" || commands[1] != "bun run typecheck" {
		t.Fatalf("commands = %#v", commands)
	}
	if len(verify) != 1 || verify[0] != "test -d node_modules" {
		t.Fatalf("verify = %#v", verify)
	}
}

func TestEnsureProjectGitignoreAddsTackRuntimeArtifacts(t *testing.T) {
	root := t.TempDir()
	gitignorePath := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := ensureProjectGitignore(root); err != nil {
		t.Fatalf("ensureProjectGitignore: %v", err)
	}
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	for _, line := range tackGitignoreBlock {
		if !strings.Contains(text, line) {
			t.Fatalf(".gitignore missing %q in %q", line, text)
		}
	}
	if err := ensureProjectGitignore(root); err != nil {
		t.Fatalf("ensureProjectGitignore second run: %v", err)
	}
	data2, _ := os.ReadFile(gitignorePath)
	if string(data2) != text {
		t.Fatalf("ensureProjectGitignore should be idempotent\nfirst:\n%s\nsecond:\n%s", text, string(data2))
	}
}

func TestInitRequiresCompletedGlobalSetup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TACK_USER_CONFIG_PATH", filepath.Join(root, "user.yaml"))
	t.Chdir(root)
	resetInitTestState(t)

	rootCmd.SetOut(&strings.Builder{})
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"init", "--non-interactive"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "global setup is incomplete") {
		t.Fatalf("init error = %v, want incomplete global setup", err)
	}
}

func TestInitValidationFailureWritesNoProjectState(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "user.yaml")
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	t.Chdir(root)
	resetInitTestState(t)
	initGateRunner = func(context.Context, string, string) error { return nil }
	writeInitUserConfig(t, userConfig, setupConfigFile{
		Setup:       config.SetupConfig{Complete: true},
		Daemon:      config.DaemonConfig{Listen: "127.0.0.1:9900"},
		Agents:      config.AgentsConfig{Runtime: "claude-code"},
		RuntimeAuth: config.RuntimeAuthConfig{Runtime: "claude-code", Provider: "anthropic", Mode: "native"},
		Models:      config.ModelsConfig{Planner: "planner", Agent: "agent", SmallTasks: "small"},
		Sandbox:     config.SandboxConfig{Provider: "local"},
		Blueprint:   "standard",
	})

	rootCmd.SetOut(&strings.Builder{})
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"init", "--non-interactive"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires quality_gates") {
		t.Fatalf("init error = %v, want quality gate validation", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".tack")); !os.IsNotExist(statErr) {
		t.Fatalf(".tack exists after failed init: %v", statErr)
	}
}

func TestInitQualityGateFailureWritesNoProjectState(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "user.yaml")
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	t.Chdir(root)
	resetInitTestState(t)
	initGateRunner = func(_ context.Context, _ string, command string) error {
		if command == "bad gate" {
			return fmt.Errorf("command not found")
		}
		return nil
	}
	writeInitUserConfig(t, userConfig, setupConfigFile{
		Setup:        config.SetupConfig{Complete: true},
		Daemon:       config.DaemonConfig{Listen: "127.0.0.1:9900"},
		Agents:       config.AgentsConfig{Runtime: "claude-code"},
		RuntimeAuth:  config.RuntimeAuthConfig{Runtime: "claude-code", Provider: "anthropic", Mode: "native"},
		Models:       config.ModelsConfig{Planner: "planner", Agent: "agent", SmallTasks: "small"},
		Sandbox:      config.SandboxConfig{Provider: "local"},
		Blueprint:    "custom-no-pr",
		QualityGates: []string{"bad gate"},
	})

	rootCmd.SetOut(&strings.Builder{})
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"init", "--non-interactive"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "quality_gates[0] \"bad gate\" failed") {
		t.Fatalf("init error = %v, want quality gate failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".tack")); !os.IsNotExist(statErr) {
		t.Fatalf(".tack exists after failed init: %v", statErr)
	}
}

func TestInitRegistersProjectAndReportsSources(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "user.yaml")
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	t.Chdir(root)
	resetInitTestState(t)
	initGateRunner = func(context.Context, string, string) error { return nil }
	writeInitUserConfig(t, userConfig, setupConfigFile{
		Setup:        config.SetupConfig{Complete: true},
		Daemon:       config.DaemonConfig{Listen: "127.0.0.1:9900"},
		Agents:       config.AgentsConfig{Runtime: "claude-code"},
		RuntimeAuth:  config.RuntimeAuthConfig{Runtime: "claude-code", Provider: "anthropic", Mode: "native"},
		Models:       config.ModelsConfig{Planner: "global-planner", Agent: "global-agent", SmallTasks: "global-small"},
		Sandbox:      config.SandboxConfig{Provider: "local"},
		Blueprint:    "custom-no-pr",
		QualityGates: []string{"go test ./..."},
	})

	var registered clientProjectRegistration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/register" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&registered); err != nil {
			t.Fatalf("Decode registration: %v", err)
		}
		_ = json.NewEncoder(w).Encode(domain.Project{ID: registered.ProjectID, RootPath: registered.RootPath, ConfigPath: registered.ConfigPath, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}))
	defer server.Close()
	daemonURL = server.URL

	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"init", "--non-interactive", "--agent-model", "project-agent", "--setup-command", "bun install"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if registered.ProjectID == "" || registered.RootPath != root || registered.ConfigPath != filepath.Join(root, ".tack", "config.yaml") {
		t.Fatalf("registration = %#v", registered)
	}
	if _, err := os.Stat(filepath.Join(root, ".tack", "project-id")); err != nil {
		t.Fatalf("project-id not written: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "models.agent: project-agent (source: project_override)") {
		t.Fatalf("summary missing project override source:\n%s", text)
	}
	if !strings.Contains(text, "models.planner: global-planner (source: global)") {
		t.Fatalf("summary missing global fallback source:\n%s", text)
	}
}

type clientProjectRegistration struct {
	ProjectID  string `json:"project_id"`
	RootPath   string `json:"root_path"`
	ConfigPath string `json:"config_path"`
}

func writeInitUserConfig(t *testing.T, path string, state setupConfigFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := yaml.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func resetInitTestState(t *testing.T) {
	t.Helper()
	rootCmd.SetArgs(nil)
	cfgPath = ""
	daemonURL = defaultDaemonURL
	projectID = ""
	initNonInteractive = false
	initRuntime = ""
	initProvider = ""
	initAuthMode = ""
	initAuthMethod = ""
	initCredentialRef = ""
	initAgentModel = ""
	initPlannerModel = ""
	initSmallTaskModel = ""
	initSandboxProvider = ""
	initBlueprint = ""
	initQualityGates = nil
	initSetupCommands = nil
	initSetupVerify = nil
	initGateRunner = runInitQualityGate
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		cfgPath = ""
		daemonURL = defaultDaemonURL
		projectID = ""
		initGateRunner = runInitQualityGate
	})
}
