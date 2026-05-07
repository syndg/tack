package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlueprintStepsOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"blueprint", "steps"})
	t.Cleanup(resetBlueprintTestState)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("blueprint steps: %v\nstderr: %s", err, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"id=plan", "type=agent", "id=create_pr", "requirements=[git,create_pr]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestBlueprintRequirementsCanRemoveCreatePR(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"blueprint", "requirements", "--remove", "create_pr"})
	t.Cleanup(resetBlueprintTestState)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("blueprint requirements: %v\nstderr: %s", err, stderr.String())
	}
	var req struct {
		CreatePR bool `json:"create_pr"`
		Git      bool `json:"git"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &req); err != nil {
		t.Fatalf("Unmarshal requirements: %v\n%s", err, stdout.String())
	}
	if req.CreatePR || !req.Git {
		t.Fatalf("requirements = %#v", req)
	}
}

func TestBlueprintSaveGlobalWritesOverride(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"blueprint", "save-global", "--remove", "create_pr"})
	t.Cleanup(resetBlueprintTestState)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("blueprint save-global: %v\nstderr: %s", err, stderr.String())
	}
	path := filepath.Join(filepath.Dir(userConfig), "blueprints", "standard.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile override: %v", err)
	}
	if !strings.Contains(stdout.String(), path) || strings.Contains(string(data), "action: create_pr") {
		t.Fatalf("override unexpected stdout=%q data=\n%s", stdout.String(), string(data))
	}
}

func resetBlueprintTestState() {
	rootCmd.SetArgs(nil)
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	blueprintRemove = nil
	blueprintSavePath = ""
	cfgPath = ""
}
