package blueprintconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/harness/blueprint"
)

func TestLoadShippedStandardRowsExposeNeutralMetadata(t *testing.T) {
	bp, err := LoadShippedStandard()
	if err != nil {
		t.Fatalf("LoadShippedStandard: %v", err)
	}
	if bp.ID != StandardBlueprintID {
		t.Fatalf("id = %q, want %q", bp.ID, StandardBlueprintID)
	}
	rows := Rows(bp)
	if len(rows) != len(bp.Steps) {
		t.Fatalf("rows = %d, want %d", len(rows), len(bp.Steps))
	}
	var foundCreatePR bool
	for _, row := range rows {
		if row.ID == "create_pr" {
			foundCreatePR = true
			if row.Action != "create_pr" || !contains(row.Dependencies, "merge.next") || !contains(row.Provides, "pull_request") || !contains(row.Requirements, "create_pr") {
				t.Fatalf("create_pr row missing metadata: %#v", row)
			}
		}
	}
	if !foundCreatePR {
		t.Fatalf("create_pr row not found")
	}
}

func TestCandidateRejectsInvalidPlanningRemoval(t *testing.T) {
	bp, err := LoadShippedStandard()
	if err != nil {
		t.Fatalf("LoadShippedStandard: %v", err)
	}
	_, err = CandidateFromRemoved(bp, []string{"plan"})
	if err == nil || !strings.Contains(err.Error(), "dependent steps remain") {
		t.Fatalf("err = %v, want dependency explanation", err)
	}
}

func TestCandidateCanRemoveCreatePRAndRequirements(t *testing.T) {
	bp, err := LoadShippedStandard()
	if err != nil {
		t.Fatalf("LoadShippedStandard: %v", err)
	}
	candidate, err := CandidateFromRemoved(bp, []string{"create_pr"})
	if err != nil {
		t.Fatalf("CandidateFromRemoved: %v", err)
	}
	if err := blueprint.Validate(candidate); err != nil {
		t.Fatalf("Validate candidate: %v", err)
	}
	if findStep(candidate, "create_pr") != nil {
		t.Fatalf("create_pr should be removed")
	}
	merge := findStep(candidate, "merge")
	if merge == nil || merge.Next != "complete" {
		t.Fatalf("merge next = %#v, want complete", merge)
	}
	req := ExtractRequirements(candidate)
	if req.CreatePR {
		t.Fatalf("CreatePR requirement should be false: %#v", req)
	}
	if !req.Runtime || !req.RuntimeAuth || !req.Sandbox || !req.Git || !req.QualityGates {
		t.Fatalf("remaining requirements missing: %#v", req)
	}
}

func TestSaveGlobalOverrideShape(t *testing.T) {
	bp, err := LoadShippedStandard()
	if err != nil {
		t.Fatalf("LoadShippedStandard: %v", err)
	}
	candidate, err := CandidateFromRemoved(bp, []string{"create_pr"})
	if err != nil {
		t.Fatalf("CandidateFromRemoved: %v", err)
	}
	path := filepath.Join(t.TempDir(), "blueprints", "standard.yaml")
	if err := SaveGlobalOverride(path, candidate); err != nil {
		t.Fatalf("SaveGlobalOverride: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "id: standard") || strings.Contains(text, "action: create_pr") {
		t.Fatalf("saved override shape unexpected:\n%s", text)
	}
}

func TestActiveRegistryRequirementsUseShippedStandard(t *testing.T) {
	reg, err := LoadActiveRegistry(filepath.Join(t.TempDir(), "config.yaml"), "")
	if err != nil {
		t.Fatalf("LoadActiveRegistry: %v", err)
	}
	req, err := ExtractRequirementsFromLookup(regLookup{reg: reg}, StandardBlueprintID)
	if err != nil {
		t.Fatalf("ExtractRequirementsFromLookup: %v", err)
	}
	if !req.CreatePR || !req.QualityGates {
		t.Fatalf("shipped standard requirements = %#v, want create_pr and quality_gates", req)
	}
}

func TestActiveRegistryRequirementsUseGlobalOverride(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "config.yaml")
	writeBlueprint(t, filepath.Join(root, "blueprints", "standard.yaml"), noPRBlueprintYAML("standard"))

	reg, err := LoadActiveRegistry(userConfig, "")
	if err != nil {
		t.Fatalf("LoadActiveRegistry: %v", err)
	}
	req, err := ExtractRequirementsFromLookup(regLookup{reg: reg}, StandardBlueprintID)
	if err != nil {
		t.Fatalf("ExtractRequirementsFromLookup: %v", err)
	}
	if req.CreatePR || req.QualityGates {
		t.Fatalf("global override requirements = %#v, want no create_pr or quality_gates", req)
	}
}

func TestActiveRegistryRequirementsUseProjectOverride(t *testing.T) {
	projectRoot := t.TempDir()
	writeBlueprint(t, filepath.Join(projectRoot, ".tack", "blueprints", "standard.yaml"), noPRBlueprintYAML("standard"))

	reg, err := LoadActiveRegistry(filepath.Join(t.TempDir(), "config.yaml"), projectRoot)
	if err != nil {
		t.Fatalf("LoadActiveRegistry: %v", err)
	}
	req, err := ExtractRequirementsFromLookup(regLookup{reg: reg}, StandardBlueprintID)
	if err != nil {
		t.Fatalf("ExtractRequirementsFromLookup: %v", err)
	}
	if req.CreatePR || req.QualityGates {
		t.Fatalf("project override requirements = %#v, want no create_pr or quality_gates", req)
	}
}

func TestExtractRequirementsFromLookupTraversesNestedBlueprintRefs(t *testing.T) {
	projectRoot := t.TempDir()
	writeBlueprint(t, filepath.Join(projectRoot, ".tack", "blueprints", "standard.yaml"), `id: standard
steps:
  - id: execute
    type: blueprint_ref
    ref: nested
`)
	writeBlueprint(t, filepath.Join(projectRoot, ".tack", "blueprints", "nested.yaml"), `id: nested
steps:
  - id: pr
    type: deterministic
    action: create_pr
`)

	reg, err := LoadActiveRegistry(filepath.Join(t.TempDir(), "config.yaml"), projectRoot)
	if err != nil {
		t.Fatalf("LoadActiveRegistry: %v", err)
	}
	req, err := ExtractRequirementsFromLookup(regLookup{reg: reg}, StandardBlueprintID)
	if err != nil {
		t.Fatalf("ExtractRequirementsFromLookup: %v", err)
	}
	if !req.CreatePR || !req.Git {
		t.Fatalf("nested requirements = %#v, want create_pr and git", req)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func findStep(bp *blueprint.Blueprint, id string) *blueprint.Step {
	for i := range bp.Steps {
		if bp.Steps[i].ID == id {
			return &bp.Steps[i]
		}
	}
	return nil
}

type regLookup struct {
	reg *blueprint.Registry
}

func (r regLookup) GetBlueprint(id string) (*blueprint.Blueprint, bool) {
	return r.reg.Get(id)
}

func writeBlueprint(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func noPRBlueprintYAML(id string) string {
	return "id: " + id + `
steps:
  - id: complete
    type: deterministic
    action: mark_complete
`
}
