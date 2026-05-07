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
