package main

import "testing"

func TestBuildProjectConfigWritesRuntimeAuthAndModels(t *testing.T) {
	configMap := buildProjectConfig(initWizardResult{
		Runtime:         "pi",
		SandboxProvider: "local",
		Provider:        "openai-codex",
		RuntimeAuthMode: "tack",
		CredentialRef:   "openai",
		AgentModel:      "gpt-5.4",
		PlannerModel:    "gpt-5.4",
		SmallTaskModel:  "gpt-5.4-mini",
		PostCreate:      "bun install",
	})
	runtimeAuth := configMap["runtime_auth"].(map[string]interface{})
	if runtimeAuth["provider"] != "openai-codex" {
		t.Fatalf("provider = %v, want openai-codex", runtimeAuth["provider"])
	}
	if runtimeAuth["credential_ref"] != "openai" {
		t.Fatalf("credential_ref = %v, want openai", runtimeAuth["credential_ref"])
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
