package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
