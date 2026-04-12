package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults_LoadsShippedWorkflows(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	ids := reg.List()
	if len(ids) != 4 {
		t.Fatalf("got %d workflows, want 4: %v", len(ids), ids)
	}
}

func TestResolveDefault_ReturnsStandardWorkflow(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, err := reg.ResolveDefault()
	if err != nil {
		t.Fatalf("ResolveDefault: %v", err)
	}
	if bp.ID != "standard" {
		t.Fatalf("default blueprint id = %q, want %q", bp.ID, "standard")
	}
	if !bp.Default {
		t.Fatal("default blueprint should have Default=true")
	}
}

func TestGet_ReturnsSpecificWorkflow(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, ok := reg.Get("build-review")
	if !ok {
		t.Fatal("Get('build-review') returned false")
	}
	if bp.Name != "Build and review" {
		t.Fatalf("name = %q, want %q", bp.Name, "Build and review")
	}

	if _, ok := reg.Get("nonexistent"); ok {
		t.Fatal("Get('nonexistent') should return false")
	}
}

func TestBuildReviewDefaultSteps(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	bp, ok := reg.Get("build-review")
	if !ok {
		t.Fatal("Get('build-review') returned false")
	}
	if len(bp.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(bp.Steps))
	}
	if bp.Steps[0].ID != "build" {
		t.Fatalf("first step id = %q, want build", bp.Steps[0].ID)
	}
}

func TestLoadFromDir_OverridesExistingByID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	dir := t.TempDir()
	custom := `id: build-review
name: Team build review
description: Custom override
steps:
  - id: custom1
    type: agent
    role: fixer
`
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(custom), 0o644); err != nil {
		t.Fatalf("writing custom workflow: %v", err)
	}

	if err := reg.LoadFromDir(dir); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	bp, ok := reg.Get("build-review")
	if !ok {
		t.Fatal("build-review not found after override")
	}
	if bp.Description != "Custom override" {
		t.Fatalf("description = %q, want %q", bp.Description, "Custom override")
	}
}

func TestLoadFromDir_RejectsMultipleDefaults(t *testing.T) {
	reg := NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	dir := t.TempDir()
	custom := `id: research
name: Research
default: true
steps:
  - id: ask
    type: agent
    role: planner
`
	if err := os.WriteFile(filepath.Join(dir, "research.yaml"), []byte(custom), 0o644); err != nil {
		t.Fatalf("writing custom workflow: %v", err)
	}

	if err := reg.LoadFromDir(dir); err == nil {
		t.Fatal("expected multiple-defaults error")
	}
}
