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
		PostCreate:      "bun install",
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
	sandbox := configMap["sandbox"].(map[string]interface{})
	postCreate := sandbox["post_create"].([]string)
	if len(postCreate) != 1 || postCreate[0] != "bun install" {
		t.Fatalf("post_create = %#v", postCreate)
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
