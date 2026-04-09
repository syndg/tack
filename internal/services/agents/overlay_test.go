package agents

import (
	"strings"
	"testing"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
)

func testObjective() *domain.Objective {
	return &domain.Objective{
		ID:          "obj-1",
		Description: "refactor authentication",
	}
}

func testStream() *domain.Stream {
	return &domain.Stream{
		ID:    "str-1",
		Title: "auth module",
	}
}

func builderRole() *RoleDefinition {
	return DefaultRoles()["builder"]
}

func TestBuildOverlay_AllSections(t *testing.T) {
	input := OverlayInput{
		AgentName:    "builder-auth-1",
		Role:         builderRole(),
		Objective:    testObjective(),
		Stream:       testStream(),
		TaskSpec:     "Implement JWT token validation",
		FileScope:    []string{"src/auth/*.go"},
		MatchedRules: []rules.MatchedRule{},
		CuratedTools: tools.CurationResult{},
		QualityGates: []string{"go test ./..."},
		LeadAgent:    "lead-auth-1",
	}

	result := BuildOverlay(input)

	sections := []string{
		"# Tack Agent: builder-auth-1",
		"## Role",
		"## Task",
		"## File Scope",
		"## Quality Gates",
		"## Communication",
		"## Constraints",
	}
	for _, section := range sections {
		if !strings.Contains(result, section) {
			t.Errorf("missing section: %q", section)
		}
	}

	if !strings.Contains(result, "refactor authentication") {
		t.Error("missing objective description")
	}
	if !strings.Contains(result, "auth module") {
		t.Error("missing stream title")
	}
	if !strings.Contains(result, "src/auth/*.go") {
		t.Error("missing file scope entry")
	}
	if !strings.Contains(result, "go test ./...") {
		t.Error("missing quality gate")
	}
	if !strings.Contains(result, "lead-auth-1") {
		t.Error("missing lead agent")
	}
	if !strings.Contains(result, "Implement JWT token validation") {
		t.Error("missing task spec")
	}
}

func TestBuildOverlay_HighPriorityRule_IMPORTANT_Prefix(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-1",
		Role:      builderRole(),
		Objective: testObjective(),
		MatchedRules: []rules.MatchedRule{
			{
				Rule: &rules.Rule{
					Scope:    "**/*.go",
					Priority: "high",
					Body:     "Always use prepared statements",
				},
			},
			{
				Rule: &rules.Rule{
					Scope:    "**/*.go",
					Priority: "normal",
					Body:     "Follow Go style guide",
				},
			},
		},
	}

	result := BuildOverlay(input)

	if !strings.Contains(result, "IMPORTANT CONSTRAINT:") {
		t.Error("missing IMPORTANT CONSTRAINT prefix for high-priority rule")
	}
	if !strings.Contains(result, "Always use prepared statements") {
		t.Error("missing high-priority rule body")
	}
	if !strings.Contains(result, "Follow Go style guide") {
		t.Error("missing normal-priority rule body")
	}

	// Verify the normal-priority rule body is NOT preceded by IMPORTANT CONSTRAINT.
	idx := strings.Index(result, "Follow Go style guide")
	if idx == -1 {
		t.Fatal("cannot find normal rule body in result")
	}
	// Check the 30 characters before the normal rule body.
	start := idx - 30
	if start < 0 {
		start = 0
	}
	preceding := result[start:idx]
	if strings.Contains(preceding, "IMPORTANT CONSTRAINT:") {
		t.Error("normal-priority rule should not have IMPORTANT CONSTRAINT prefix")
	}
}

func TestBuildOverlay_EmptyFileScope_NoSection(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-1",
		Role:      builderRole(),
		Objective: testObjective(),
		FileScope: []string{}, // empty
	}

	result := BuildOverlay(input)

	if strings.Contains(result, "## File Scope") {
		t.Error("should not include File Scope section when FileScope is empty")
	}
}

func TestBuildOverlay_EmptyLeadAgent_NoLeadLine(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-1",
		Role:      builderRole(),
		Objective: testObjective(),
		LeadAgent: "", // empty — planner-level agent
	}

	result := BuildOverlay(input)

	if strings.Contains(result, "Your lead is:") {
		t.Error("should not include lead agent line when LeadAgent is empty")
	}
}

func TestBuildOverlay_MessageMetadataInstructions(t *testing.T) {
	input := OverlayInput{
		AgentName:  "builder-1",
		Role:       builderRole(),
		Objective:  testObjective(),
		CommitMode: "auto",
		Messages: &blueprint.MessageRequests{
			Commit: true,
			PR:     true,
		},
	}

	result := BuildOverlay(input)

	if !strings.Contains(result, "## Delivery Metadata") {
		t.Fatal("missing delivery metadata section")
	}
	if !strings.Contains(result, "TACK_MESSAGES:") {
		t.Fatal("missing TACK_MESSAGES output instructions")
	}
	for _, field := range []string{"commit_message", "pr_title", "pr_body"} {
		if !strings.Contains(result, field) {
			t.Errorf("missing requested field %q", field)
		}
	}
}

func TestBuildOverlay_RetryContext(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-1",
		Role:      builderRole(),
		Objective: testObjective(),
		RetryContext: &RetryContext{
			AttemptNumber: 2,
			MaxAttempts:   3,
			FailureKind:   domain.FailureQualityGate,
			LastError:     "gate failed",
			HumanGuidance: "keep the API shape unchanged",
		},
	}

	result := BuildOverlay(input)

	for _, want := range []string{"## Retry Context", "Attempt: 2/3", "Failure kind: quality_gate_failure", "gate failed", "keep the API shape unchanged"} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildOverlay_ReviewerOutputInstructions(t *testing.T) {
	input := OverlayInput{
		AgentName: "reviewer-auth",
		Role:      DefaultRoles()["reviewer"],
		Objective: &domain.Objective{Description: "review auth flow"},
		Stream:    &domain.Stream{Title: "auth stream"},
	}

	result := BuildOverlay(input)
	for _, want := range []string{"## Review Output", "REVIEW_DECISION: approve", "REVIEW_DECISION: reject", "REVIEW_FEEDBACK:"} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildPlannerOverlay_AllSections(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, "use bun for JS")

	expected := []string{
		"# Tack Agent: planner",
		"## Role",
		"## Objective",
		"## Project Guidance",
		"## Instructions",
		"streams:",
		"quality_gates:",
	}
	for _, s := range expected {
		if !strings.Contains(result, s) {
			t.Errorf("missing: %q", s)
		}
	}
	if !strings.Contains(result, obj.Description) {
		t.Error("missing objective description")
	}
	if !strings.Contains(result, "use bun for JS") {
		t.Error("missing guidance")
	}
}

func TestBuildPlannerOverlay_EmptyGuidance_NoSection(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, "")

	if strings.Contains(result, "## Project Guidance") {
		t.Error("should not include Project Guidance section when guidance is empty")
	}
}

func TestBuildPlannerOverlay_IncludesPlanYAMLSchema(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, "")

	// Should contain a YAML code block with the plan schema.
	if !strings.Contains(result, "```yaml") {
		t.Error("missing yaml code fence in planner overlay")
	}
	if !strings.Contains(result, "file_scope:") {
		t.Error("missing file_scope in plan schema")
	}
	if !strings.Contains(result, "dependencies:") {
		t.Error("missing dependencies in plan schema")
	}
}
