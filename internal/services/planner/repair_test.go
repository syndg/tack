package planner

import (
	"strings"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestCompileStreamCardRepair_PreservesExistingFieldsAndHydratesAnchors(t *testing.T) {
	stream := domain.Stream{
		Title: "auth stream",
		Card: &domain.StreamCard{
			Goal:                "Initial goal",
			BlockedBy:           []string{"stream A"},
			AcceptanceCriteria:  []string{"existing acceptance"},
			ImplementationScope: []string{"src/auth/**"},
			ProofScope:          []string{"existing proof"},
		},
	}
	dossier := &domain.Dossier{Citations: []domain.DossierCitation{{ID: "file:1", Kind: "file", Target: "src/auth/handlers/login.go", Detail: "Login handlers already route through middleware."}}}

	card, err := CompileStreamCardRepair(`goal: "Harden auth flow"
acceptance_criteria:
  - "Requests without middleware are rejected"
hard_anchors:
  - instruction: "Route all login handlers through auth middleware"
    citation_ids:
      - "file:1"`, stream, dossier)
	if err != nil {
		t.Fatalf("CompileStreamCardRepair: %v", err)
	}
	if card.Goal != "Harden auth flow" {
		t.Fatalf("goal = %q", card.Goal)
	}
	if len(card.AcceptanceCriteria) != 1 || card.AcceptanceCriteria[0] != "Requests without middleware are rejected" {
		t.Fatalf("acceptance criteria = %#v", card.AcceptanceCriteria)
	}
	if len(card.ImplementationScope) != 1 || card.ImplementationScope[0] != "src/auth/**" {
		t.Fatalf("implementation scope = %#v", card.ImplementationScope)
	}
	if len(card.BlockedBy) != 1 || card.BlockedBy[0] != "stream A" {
		t.Fatalf("blocked_by = %#v", card.BlockedBy)
	}
	if len(card.HardAnchors) != 1 || len(card.HardAnchors[0].Citations) != 1 || card.HardAnchors[0].Citations[0].ID != "file:1" {
		t.Fatalf("hard anchors = %#v", card.HardAnchors)
	}
}

func TestCompileStreamCardRepair_RejectsUnknownCitation(t *testing.T) {
	stream := domain.Stream{Title: "auth stream", Card: &domain.StreamCard{Goal: "Initial goal"}}
	_, err := CompileStreamCardRepair(`hard_anchors:
  - instruction: "Route all login handlers through auth middleware"
    citation_ids:
      - "missing:1"`, stream, &domain.Dossier{})
	if err == nil || !strings.Contains(err.Error(), "unknown citation") {
		t.Fatalf("err = %v, want unknown citation", err)
	}
}
