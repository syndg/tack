package benchmark

import (
	"strings"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestPrepareRunUsesPromptForRun(t *testing.T) {
	run, ok := PrepareRun("lazygit.command-log-nav-keybindings", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	for _, want := range []string{
		"Use this exact benchmark decomposition to keep hardness frozen for comparison runs:",
		"`Command log scroll mechanics`",
		"`Scoped command log keybindings and panel affordances`",
		"`Regression coverage and keybinding reference sync`",
	} {
		if !strings.Contains(run.PromptSnapshot, want) {
			t.Fatalf("prompt snapshot missing %q\n%s", want, run.PromptSnapshot)
		}
	}
}

func TestPrepareRunUsesPromptForRunUndoBenchmark(t *testing.T) {
	run, ok := PrepareRun("lazygit.undo-basic-commit-checkout", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	for _, want := range []string{
		"Use this exact benchmark decomposition to keep hardness frozen for comparison runs:",
		"Keep the end-to-end validation entrypoints `undo/undo_commit` and `reflog/checkout` intact:",
		"preserving unrelated working-tree changes",
		"`Narrow reflog undo core to plain commit and checkout`",
		"`Exercise supported and unsupported undo flows in integration tests`",
		"`Update user-facing docs and copy for the benchmark slice`",
	} {
		if !strings.Contains(run.PromptSnapshot, want) {
			t.Fatalf("prompt snapshot missing %q\n%s", want, run.PromptSnapshot)
		}
	}
}

func TestSpecValidatePlanShape(t *testing.T) {
	spec, ok := FindSpec("lazygit.command-log-nav-keybindings")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	streams := []domain.Stream{
		{Title: "Command log scroll mechanics", Card: &domain.StreamCard{}},
		{Title: "Scoped command log keybindings and panel affordances", Card: &domain.StreamCard{BlockedBy: []string{"Command log scroll mechanics"}}},
		{Title: "Regression coverage and keybinding reference sync", Card: &domain.StreamCard{BlockedBy: []string{"Scoped command log keybindings and panel affordances"}}},
	}
	if err := spec.ValidatePlanShape(streams); err != nil {
		t.Fatalf("ValidatePlanShape: %v", err)
	}
	streams[2].Title = "Docs only"
	if err := spec.ValidatePlanShape(streams); err == nil {
		t.Fatal("expected plan shape mismatch")
	}
}

func TestSpecValidatePlanShapeUndoBenchmark(t *testing.T) {
	spec, ok := FindSpec("lazygit.undo-basic-commit-checkout")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	streams := []domain.Stream{
		{Title: "Narrow reflog undo core to plain commit and checkout", Card: &domain.StreamCard{}},
		{Title: "Exercise supported and unsupported undo flows in integration tests", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
		{Title: "Update user-facing docs and copy for the benchmark slice", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
	}
	if err := spec.ValidatePlanShape(streams); err != nil {
		t.Fatalf("ValidatePlanShape: %v", err)
	}
	streams[1].Card = &domain.StreamCard{BlockedBy: []string{"Exercise supported and unsupported undo flows in integration tests"}}
	if err := spec.ValidatePlanShape(streams); err == nil {
		t.Fatal("expected plan shape mismatch")
	}
}
