package agents

import "testing"

func TestParseContractOutcomeBlocked(t *testing.T) {
	outcome, ok := ParseContractOutcome("CONTRACT_OUTCOME: contract_blocked\nCONTRACT_REASON: Missing ownership rule for auth middleware")
	if !ok {
		t.Fatal("expected structured contract outcome")
	}
	if outcome.Kind != ContractOutcomeBlocked {
		t.Fatalf("kind = %q, want %q", outcome.Kind, ContractOutcomeBlocked)
	}
	if outcome.Reason != "Missing ownership rule for auth middleware" {
		t.Fatalf("reason = %q", outcome.Reason)
	}
}

func TestParseContractOutcomeGapWithStreamCard(t *testing.T) {
	outcome, ok := ParseContractOutcome("CONTRACT_OUTCOME: contract_gap\nCONTRACT_REASON: Dossier shows middleware must wrap login handlers\nCONTRACT_STREAM_CARD:\n```yaml\ngoal: \"Harden auth flow\"\nacceptance_criteria:\n  - \"Requests without middleware are rejected\"\n```")
	if !ok {
		t.Fatal("expected structured contract outcome")
	}
	if outcome.Kind != ContractOutcomeGap {
		t.Fatalf("kind = %q, want %q", outcome.Kind, ContractOutcomeGap)
	}
	if outcome.Reason != "Dossier shows middleware must wrap login handlers" {
		t.Fatalf("reason = %q", outcome.Reason)
	}
	if outcome.StreamCardYAML != "goal: \"Harden auth flow\"\nacceptance_criteria:\n  - \"Requests without middleware are rejected\"" {
		t.Fatalf("stream card = %q", outcome.StreamCardYAML)
	}
}
