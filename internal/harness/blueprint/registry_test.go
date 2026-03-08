package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults_LoadsThreeBlueprints(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	names := reg.List()
	if len(names) != 3 {
		t.Fatalf("got %d blueprints, want 3: %v", len(names), names)
	}
}

func TestGetDefault_ReturnsFeature(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, ok := reg.GetDefault()
	if !ok {
		t.Fatal("GetDefault returned false")
	}
	if bp.Name != "Feature Implementation" {
		t.Errorf("default blueprint name = %q, want %q", bp.Name, "Feature Implementation")
	}
	if bp.Trigger != "default" {
		t.Errorf("trigger = %q, want %q", bp.Trigger, "default")
	}
}

func TestGet_ReturnsSpecificBlueprint(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, ok := reg.Get("Hotfix")
	if !ok {
		t.Fatal("Get('Hotfix') returned false")
	}
	if bp.Name != "Hotfix" {
		t.Errorf("name = %q, want %q", bp.Name, "Hotfix")
	}

	_, ok = reg.Get("Nonexistent")
	if ok {
		t.Error("Get('Nonexistent') should return false")
	}
}

func TestStreamExecutionDefaultIncludesOptionalScout(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, ok := reg.Get("Stream Execution")
	if !ok {
		t.Fatal("Get('Stream Execution') returned false")
	}
	if len(bp.Steps) != 5 {
		t.Fatalf("steps = %d, want 5", len(bp.Steps))
	}

	first := bp.Steps[0]
	if first.ID != "scout" {
		t.Fatalf("first step id = %q, want %q", first.ID, "scout")
	}
	if first.Type != StepTypeAgent {
		t.Fatalf("first step type = %q, want %q", first.Type, StepTypeAgent)
	}
	if first.Role != "scout" {
		t.Fatalf("first step role = %q, want %q", first.Role, "scout")
	}
	if !first.Optional {
		t.Fatal("expected scout step to be optional")
	}
	if first.Next != "build" {
		t.Fatalf("first step next = %q, want %q", first.Next, "build")
	}
}

func TestLoadFromDir_OverridesExisting(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	// Create a custom blueprint that overrides "Hotfix".
	dir := t.TempDir()
	custom := `name: Hotfix
description: Custom override
trigger: manual
steps:
  - id: custom1
    type: agent
    role: fixer
`
	if err := os.WriteFile(filepath.Join(dir, "hotfix.yaml"), []byte(custom), 0644); err != nil {
		t.Fatalf("writing custom: %v", err)
	}

	if err := reg.LoadFromDir(dir); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	bp, ok := reg.Get("Hotfix")
	if !ok {
		t.Fatal("Hotfix not found after override")
	}
	if bp.Description != "Custom override" {
		t.Errorf("description = %q, want %q", bp.Description, "Custom override")
	}
}
