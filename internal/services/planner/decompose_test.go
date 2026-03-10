package planner

import (
	"strings"
	"testing"
)

func TestParsePlan_WithCodeBlock(t *testing.T) {
	output := "Here is the plan:\n\n```yaml\nstreams:\n  - title: \"auth service\"\n    description: \"Handle auth\"\n    file_scope:\n      - \"src/auth/**\"\n    dependencies: []\nquality_gates:\n  - \"go test ./...\"\n```\nDone."

	plan, err := ParsePlan(output)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(plan.Streams) != 1 {
		t.Fatalf("streams len = %d, want 1", len(plan.Streams))
	}
	if plan.Streams[0].Title != "auth service" {
		t.Errorf("title = %q, want \"auth service\"", plan.Streams[0].Title)
	}
	if len(plan.QualityGates) != 1 || plan.QualityGates[0] != "go test ./..." {
		t.Errorf("quality_gates = %v, want [\"go test ./...\"]", plan.QualityGates)
	}
}

func TestParsePlan_RawYAML(t *testing.T) {
	output := "streams:\n  - title: \"db migration\"\n    description: \"Migrate database\"\n    file_scope:\n      - \"db/**\"\n    dependencies: []\nquality_gates: []"

	plan, err := ParsePlan(output)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(plan.Streams) != 1 {
		t.Fatalf("streams len = %d, want 1", len(plan.Streams))
	}
	if plan.Streams[0].Title != "db migration" {
		t.Errorf("title = %q, want \"db migration\"", plan.Streams[0].Title)
	}
}

func TestValidatePlan_NoStreams(t *testing.T) {
	plan := &RawPlan{Streams: []RawStream{}}
	if err := ValidatePlan(plan); err == nil {
		t.Error("expected error for empty streams")
	}
}

func TestValidatePlan_MissingTitle(t *testing.T) {
	plan := &RawPlan{
		Streams: []RawStream{
			{Title: "", FileScope: []string{"src/**"}},
		},
	}
	err := ValidatePlan(plan)
	if err == nil {
		t.Error("expected error for missing title")
	}
	if !strings.Contains(err.Error(), "missing title") {
		t.Errorf("error = %q, want to contain 'missing title'", err.Error())
	}
}

func TestValidatePlan_MissingFileScope(t *testing.T) {
	plan := &RawPlan{
		Streams: []RawStream{
			{Title: "auth", FileScope: []string{}},
		},
	}
	err := ValidatePlan(plan)
	if err == nil {
		t.Error("expected error for missing file_scope")
	}
	if !strings.Contains(err.Error(), "missing file_scope") {
		t.Errorf("error = %q, want to contain 'missing file_scope'", err.Error())
	}
}

func TestValidatePlan_DanglingDependency(t *testing.T) {
	plan := &RawPlan{
		Streams: []RawStream{
			{Title: "auth", FileScope: []string{"src/auth/**"}, Dependencies: []string{"nonexistent"}},
		},
	}
	err := ValidatePlan(plan)
	if err == nil {
		t.Error("expected error for dangling dependency")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestDetectCycles_CircularDependency(t *testing.T) {
	streams := []RawStream{
		{Title: "a", Dependencies: []string{"b"}},
		{Title: "b", Dependencies: []string{"a"}},
	}
	if err := DetectCycles(streams); err == nil {
		t.Error("expected error for circular dependency")
	}
}

func TestDetectCycles_ThreeWayCycle(t *testing.T) {
	streams := []RawStream{
		{Title: "a", Dependencies: []string{"c"}},
		{Title: "b", Dependencies: []string{"a"}},
		{Title: "c", Dependencies: []string{"b"}},
	}
	if err := DetectCycles(streams); err == nil {
		t.Error("expected error for three-way cycle")
	}
}

func TestDetectCycles_NoCycles(t *testing.T) {
	streams := []RawStream{
		{Title: "a", Dependencies: []string{}},
		{Title: "b", Dependencies: []string{"a"}},
		{Title: "c", Dependencies: []string{"a", "b"}},
	}
	if err := DetectCycles(streams); err != nil {
		t.Errorf("unexpected cycle error: %v", err)
	}
}

func TestToDomain_GeneratesIDs(t *testing.T) {
	raw := &RawPlan{
		Streams: []RawStream{
			{Title: "s1", FileScope: []string{"src/**"}, Dependencies: []string{}},
			{Title: "s2", FileScope: []string{"lib/**"}, Dependencies: []string{"s1"}},
		},
		QualityGates: []string{"go test"},
	}

	plan, streams := ToDomain(raw, "obj-123")

	if plan.ID == "" {
		t.Error("plan ID should be generated")
	}
	if plan.ObjectiveID != "obj-123" {
		t.Errorf("ObjectiveID = %q, want \"obj-123\"", plan.ObjectiveID)
	}
	if len(streams) != 2 {
		t.Fatalf("streams len = %d, want 2", len(streams))
	}

	s1 := streams[0]
	s2 := streams[1]
	if s1.ID == "" || s2.ID == "" {
		t.Error("stream IDs should be generated")
	}
	if s1.PlanID != plan.ID || s2.PlanID != plan.ID {
		t.Error("streams should reference the plan ID")
	}

	// s2 depends on s1 — dependency title "s1" should be resolved to s1.ID.
	if len(s2.Dependencies) != 1 {
		t.Fatalf("s2 dependencies len = %d, want 1", len(s2.Dependencies))
	}
	if s2.Dependencies[0] != s1.ID {
		t.Errorf("s2.Dependencies[0] = %q, want s1.ID (%q)", s2.Dependencies[0], s1.ID)
	}
}

func TestToDomain_InitialStatuses(t *testing.T) {
	raw := &RawPlan{
		Streams: []RawStream{
			{Title: "s1", FileScope: []string{"src/**"}, Dependencies: []string{}},
		},
	}

	plan, streams := ToDomain(raw, "obj-456")

	if plan.Status != "draft" {
		t.Errorf("plan status = %q, want draft", plan.Status)
	}
	if streams[0].Status != "pending" {
		t.Errorf("stream status = %q, want pending", streams[0].Status)
	}
}
