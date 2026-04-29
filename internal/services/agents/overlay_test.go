package agents

import (
	"strings"
	"testing"

	"github.com/syndg/tack/internal/contractpatch"
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
		Card: &domain.StreamCard{
			Goal:                "Implement JWT middleware enforcement",
			BlockedBy:           []string{"auth discovery"},
			AcceptanceCriteria:  []string{"JWT validation passes for valid tokens", "Requests without valid tokens are rejected"},
			ImplementationScope: []string{"src/auth/*.go"},
			ProofScope:          []string{"go test ./internal/auth"},
			HardAnchors: []domain.StreamCardAnchor{{
				Instruction: "Keep auth checks in middleware",
				Citations:   []domain.StreamCardCitation{{ID: "rule:1", Kind: "rule", Target: ".tack/rules/auth.md", Detail: "Keep auth checks in middleware."}},
			}},
		},
		AcceptanceCriteria: []string{
			"JWT validation passes for valid tokens",
			"Requests without valid tokens are rejected",
		},
	}
}

func testDossier() *domain.Dossier {
	return &domain.Dossier{
		Summary:        "Discovery resolved likely auth files and seams.",
		RepoPriors:     []domain.DossierPrior{{Kind: "rule", Title: "auth.md", Detail: "Keep auth checks in middleware."}},
		RelevantFiles:  []domain.DossierReference{{Path: "src/auth/middleware.go", Reason: "path matches auth"}},
		SuggestedSeams: []domain.DossierSeam{{Title: "src/auth", Reason: "Relevant files cluster under src/auth.", FilePaths: []string{"src/auth/middleware.go"}}},
		Risks:          []string{"Token refresh flow is under-specified."},
		Unknowns:       []string{"Which handlers still bypass middleware?"},
		Citations:      []domain.DossierCitation{{ID: "rule:1", Kind: "rule", Target: ".tack/rules/auth.md", Detail: "Keep auth checks in middleware."}},
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
		"## Blocked By",
		"## Acceptance Criteria",
		"## Implementation Scope",
		"## Proof Scope",
		"## Hard Anchors",
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
	if !strings.Contains(result, "satisfy this stream card only") {
		t.Error("missing stream responsibility boundary")
	}
	if !strings.Contains(result, "Treat the Objective as context") {
		t.Error("missing objective-as-context constraint")
	}
	if !strings.Contains(result, "Keep auth checks in middleware") {
		t.Error("missing hard anchor")
	}
	if !strings.Contains(result, "rule:1") {
		t.Error("missing hard anchor citation")
	}
	for _, want := range []string{"JWT validation passes for valid tokens", "Requests without valid tokens are rejected"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing acceptance criteria %q", want)
		}
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
	for _, want := range []string{"## Review Output", "REVIEW_DECISION: approve", "REVIEW_DECISION: reject", "REVIEW_FEEDBACK:", "CONTRACT_OUTCOME: contract_gap", "CONTRACT_STREAM_CARD:"} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildOverlay_BuilderContractBlockedInstructions(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-auth",
		Role:      DefaultRoles()["builder"],
		Objective: &domain.Objective{Description: "build auth flow"},
		Stream: &domain.Stream{
			Title: "auth stream",
			Card: &domain.StreamCard{
				Goal: "Harden auth flow",
			},
		},
	}

	result := BuildOverlay(input)
	for _, want := range []string{"## Contract Failure Output", "CONTRACT_OUTCOME: contract_blocked", "CONTRACT_REASON:"} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildOverlay_IncludesObjectiveInsights(t *testing.T) {
	input := OverlayInput{
		AgentName: "builder-auth",
		Role:      DefaultRoles()["builder"],
		Objective: &domain.Objective{Description: "build auth flow"},
		Stream: &domain.Stream{
			ID:    "stream-1",
			Title: "auth stream",
			Card:  &domain.StreamCard{Goal: "Harden auth flow"},
		},
		ObjectiveInsights: []domain.ObjectiveInsight{{
			Source:  domain.InsightSourceReviewer,
			Kind:    domain.InsightKindReviewRejection,
			Summary: "Reviewer requested regression coverage",
			Detail:  "Add coverage for the error path before merge.",
		}},
	}

	result := BuildOverlay(input)
	for _, want := range []string{"## Contract Patches", "Preserve this previously rejected requirement: Reviewer requested regression coverage", "Rationale: Add coverage for the error path before merge.", "## Objective Insights", "[reviewer/review_rejection] Reviewer requested regression coverage", "Detail: Add coverage for the error path before merge."} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestDeriveContractPatches_FiltersOtherStreamsAndDeduplicates(t *testing.T) {
	stream := &domain.Stream{ID: "stream-1", Title: "auth stream"}
	patches := contractpatch.Compile([]domain.ObjectiveInsight{
		{StreamID: "stream-1", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Add regression coverage", Detail: "Add regression coverage for auth expiry."},
		{StreamID: "stream-2", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Other stream finding", Detail: "Ignore me."},
		{Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance, Summary: "Keep API stable"},
		{Source: domain.InsightSourceHuman, Kind: domain.InsightKindRetryGuidance, Summary: "Keep API stable"},
	}, stream.ID)
	if len(patches) != 2 {
		t.Fatalf("patch count = %d, want 2: %#v", len(patches), patches)
	}
	if !strings.Contains(patches[0].Instruction, "Add regression coverage") {
		t.Fatalf("patch[0] = %+v", patches[0])
	}
	if !strings.Contains(patches[0].Rationale, "auth expiry") {
		t.Fatalf("patch[0] rationale = %+v", patches[0])
	}
	if !strings.Contains(patches[1].Instruction, "Keep API stable") {
		t.Fatalf("patch[1] = %+v", patches[1])
	}
}

func TestBuildOverlay_AutoAppliesRepeatedObjectiveCandidates(t *testing.T) {
	stream := testStream()
	stream.ID = "stream-1"
	result := BuildOverlay(OverlayInput{
		AgentName: "builder-auth-1",
		Objective: testObjective(),
		Stream:    stream,
		Role:      builderRole(),
		ObjectiveInsights: []domain.ObjectiveInsight{
			{StreamID: "stream-2", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit"},
			{StreamID: "stream-3", Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit"},
		},
	})
	for _, want := range []string{"[codification_candidate/", "Evidence count: 2", "Preserve this previously rejected requirement: Keep auth middleware coverage explicit"} {
		if !strings.Contains(result, want) {
			t.Fatalf("overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildPlannerOverlay_AllSections(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, testDossier(), "use bun for JS", []domain.ObjectiveInsight{{
		Source:  domain.InsightSourceHuman,
		Kind:    domain.InsightKindRetryGuidance,
		Summary: "Keep auth compatibility stable",
	}})

	expected := []string{
		"# Tack Agent: planner",
		"## Role",
		"## Objective",
		"## Dossier Summary",
		"## Dossier Citations",
		"## Contract Patches",
		"Honor this explicit human retry guidance: Keep auth compatibility stable",
		"## Objective Insights",
		"## Relevant Files",
		"## Suggested Seams",
		"## Project Guidance",
		"## Instructions",
		"File scopes are enforced",
		"Do not invent name-derived globs",
		"Keep tightly coupled source-and-test edits in one stream",
		"streams:",
		"hard_anchors:",
		"acceptance_criteria:",
		"quality_gates:",
		"PLANNER_OUTCOME: needs_dossier_expansion",
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
	if !strings.Contains(result, "Do not explore the codebase") {
		t.Error("missing dossier-only constraint")
	}
}

func TestBuildOverlays_DoNotInjectApprovedProjectMemoryPromotionsInV1(t *testing.T) {
	promotion := domain.PromotionRecord{
		Target:  domain.PromotionTargetProjectMemory,
		Status:  domain.PromotionStatusApproved,
		Summary: "Durable project memory must not enter v1 prompts",
	}
	objective := testObjective()
	stream := testStream()

	outputs := map[string]string{
		"planner": BuildPlannerOverlay(objective, testDossier(), "", nil),
		"builder": BuildOverlay(OverlayInput{
			AgentName: "builder-auth",
			Role:      DefaultRoles()["builder"],
			Objective: objective,
			Stream:    stream,
		}),
		"reviewer": BuildOverlay(OverlayInput{
			AgentName: "reviewer-auth",
			Role:      DefaultRoles()["reviewer"],
			Objective: objective,
			Stream:    stream,
		}),
	}
	for name, output := range outputs {
		if strings.Contains(output, promotion.Summary) || strings.Contains(output, string(promotion.Target)) {
			t.Fatalf("%s overlay injected approved project-memory promotion:\n%s", name, output)
		}
	}
}

func TestBuildOverlay_ReviewerTreatsOutOfScopeFixAsContractGap(t *testing.T) {
	result := BuildOverlay(OverlayInput{
		AgentName: "reviewer-auth-1",
		Role:      DefaultRoles()["reviewer"],
		Objective: testObjective(),
		Stream:    testStream(),
		FileScope: []string{"src/auth/config.go"},
	})

	for _, want := range []string{"requires changing files outside this stream's File Scope", "contract gap", "Do not spend retry budget"} {
		if !strings.Contains(result, want) {
			t.Fatalf("reviewer overlay missing %q\n%s", want, result)
		}
	}
}

func TestBuildPlannerOverlay_EmptyGuidance_NoSection(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, testDossier(), "", nil)

	if strings.Contains(result, "## Project Guidance") {
		t.Error("should not include Project Guidance section when guidance is empty")
	}
}

func TestBuildPlannerOverlay_IncludesPlanYAMLSchema(t *testing.T) {
	obj := testObjective()
	result := BuildPlannerOverlay(obj, testDossier(), "", nil)

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
