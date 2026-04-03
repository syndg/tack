package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFile_Valid(t *testing.T) {
	dir := t.TempDir()
	yaml := `id: test-workflow
name: Test Blueprint
description: A test workflow
steps:
  - id: step1
    type: agent
    role: planner
    next: step2
  - id: step2
    type: deterministic
    action: lint
`
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	bp, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if bp.Name != "Test Blueprint" {
		t.Errorf("name = %q, want %q", bp.Name, "Test Blueprint")
	}
	if len(bp.Steps) != 2 {
		t.Errorf("steps = %d, want 2", len(bp.Steps))
	}
	if bp.Steps[0].Type != StepTypeAgent {
		t.Errorf("step[0].Type = %q, want %q", bp.Steps[0].Type, StepTypeAgent)
	}
	if bp.ID != "test-workflow" {
		t.Errorf("id = %q, want %q", bp.ID, "test-workflow")
	}
}

func TestValidate_DuplicateStepIDs(t *testing.T) {
	bp := &Blueprint{
		ID:   "dup",
		Name: "dup",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "a"},
			{ID: "s1", Type: StepTypeAgent, Role: "b"},
		},
	}
	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for duplicate step IDs")
	}
	if !strings.Contains(err.Error(), "duplicate step ID") {
		t.Errorf("error = %q, want to contain 'duplicate step ID'", err.Error())
	}
}

func TestValidate_DanglingNextRef(t *testing.T) {
	bp := &Blueprint{
		ID:   "dangle",
		Name: "dangle",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "a", Next: "nonexistent"},
		},
	}
	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for dangling next ref")
	}
	if !strings.Contains(err.Error(), "non-existent next step") {
		t.Errorf("error = %q, want to contain 'non-existent next step'", err.Error())
	}
}

func TestValidate_AgentWithoutRole(t *testing.T) {
	bp := &Blueprint{
		ID:   "no-role",
		Name: "no-role",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent},
		},
	}
	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for agent step without role")
	}
	if !strings.Contains(err.Error(), "must have a role") {
		t.Errorf("error = %q, want to contain 'must have a role'", err.Error())
	}
}

func TestValidate_DeterministicWithoutAction(t *testing.T) {
	bp := &Blueprint{
		ID:   "no-action",
		Name: "no-action",
		Steps: []Step{
			{ID: "s1", Type: StepTypeDeterministic},
		},
	}
	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for deterministic step without action")
	}
	if !strings.Contains(err.Error(), "must have an action") {
		t.Errorf("error = %q, want to contain 'must have an action'", err.Error())
	}
}

func TestValidate_RejectsEmptyMessagesConfig(t *testing.T) {
	bp := &Blueprint{
		ID:   "empty-messages",
		Name: "empty-messages",
		Steps: []Step{
			{ID: "s1", Type: StepTypeAgent, Role: "builder", Messages: &MessageRequests{}},
		},
	}

	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for empty messages config")
	}
	if !strings.Contains(err.Error(), "empty messages config") {
		t.Errorf("error = %q, want to contain 'empty messages config'", err.Error())
	}
}

func TestValidate_RejectsUnknownMessageSource(t *testing.T) {
	bp := &Blueprint{
		ID:   "unknown-message-source",
		Name: "unknown-message-source",
		Steps: []Step{
			{ID: "fix", Type: StepTypeAgent, Role: "builder"},
			{ID: "pr", Type: StepTypeDeterministic, Action: "create_pr", MessageSource: "missing"},
		},
	}

	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for unknown message source")
	}
	if !strings.Contains(err.Error(), "non-existent message source") {
		t.Errorf("error = %q, want to contain 'non-existent message source'", err.Error())
	}
}

func TestValidate_UnreachableStepChain(t *testing.T) {
	bp := &Blueprint{
		ID:   "unreachable",
		Name: "unreachable",
		Steps: []Step{
			{ID: "entry", Type: StepTypeAgent, Role: "planner", Next: "done"},
			{ID: "done", Type: StepTypeDeterministic, Action: "finish"},
			{ID: "orphan", Type: StepTypeAgent, Role: "builder", Next: "orphan_done"},
			{ID: "orphan_done", Type: StepTypeDeterministic, Action: "noop"},
		},
	}

	err := Validate(bp)
	if err == nil {
		t.Fatal("expected error for unreachable steps")
	}
	if !strings.Contains(err.Error(), `step "orphan" is unreachable`) {
		t.Fatalf("error = %q, want orphan step to be unreachable", err.Error())
	}
	if !strings.Contains(err.Error(), `step "orphan_done" is unreachable`) {
		t.Fatalf("error = %q, want orphan_done step to be unreachable", err.Error())
	}
}

func TestLoadDir_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	bp1 := `id: alpha
name: Alpha
steps:
  - id: a1
    type: agent
    role: dev
`
	bp2 := `id: beta
name: Beta
steps:
  - id: b1
    type: deterministic
    action: build
`
	os.WriteFile(filepath.Join(dir, "alpha.yaml"), []byte(bp1), 0644)
	os.WriteFile(filepath.Join(dir, "beta.yml"), []byte(bp2), 0644)
	// Non-YAML files should be ignored.
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0644)

	bps, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bps) != 2 {
		t.Fatalf("got %d blueprints, want 2", len(bps))
	}
	if _, ok := bps["alpha"]; !ok {
		t.Error("missing workflow 'alpha'")
	}
	if _, ok := bps["beta"]; !ok {
		t.Error("missing workflow 'beta'")
	}
}
