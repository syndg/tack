package benchmark

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
)

type Spec struct {
	ID            string
	DisplayName   string
	Repo          string
	RepoURL       string
	DefaultBranch string
	Blueprint     string
	Family        string
	Tags          []string
	QualityGates  []string
	Baseline      string
	FeatureRange  string
	ContextPolicy string
	Prompt        string
	Validation    string
	Outstanding   []string
	ExpectedPlan  *ExpectedPlan
}

type ExpectedPlan struct {
	Streams []ExpectedStream
}

type ExpectedStream struct {
	Title     string
	BlockedBy []string
}

func BuiltInSpecs() []Spec {
	return []Spec{
		{
			ID:            "lazygit.undo-basic-commit-checkout",
			DisplayName:   "Lazygit undo commit and checkout",
			Repo:          "lazygit",
			RepoURL:       "https://github.com/jesseduffield/lazygit.git",
			DefaultBranch: "master",
			Blueprint:     "benchmark-baseline",
			Family:        "feature-resurrection",
			Tags:          []string{"baseline", "go", "tui"},
			QualityGates: []string{
				"go test ./pkg/gui/controllers -count=1",
				"go test ./pkg/commands/git_commands -count=1",
			},
			Baseline:      "43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
			FeatureRange:  "4065175a5811d688adc65b4b974f6fced0cdba67",
			ContextPolicy: "repo-only-no-history",
			Prompt:        "Match lazygit's canonical reflog undo slice for recent plain commit and checkout actions. Keep the end-to-end validation entrypoints `undo/undo_commit` and `reflog/checkout` intact: `undo/undo_commit` must preserve the canonical undo/redo commit behavior, including restoring file state while preserving unrelated working-tree changes, and `reflog/checkout` must continue to pass. Explicitly exclude pull --rebase, merge, revert, amend/reword, fixup/squash, and broader rebase flows from the supported feature surface.",
			Validation:    "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v",
			ExpectedPlan: &ExpectedPlan{Streams: []ExpectedStream{
				{Title: "Narrow reflog undo core to plain commit and checkout"},
				{Title: "Exercise supported and unsupported undo flows in integration tests", BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}},
				{Title: "Update user-facing docs and copy for the benchmark slice", BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}},
			}},
		},
		{
			ID:            "lazygit.command-log-nav-keybindings",
			DisplayName:   "Lazygit command log navigation keybindings",
			Repo:          "lazygit",
			RepoURL:       "https://github.com/jesseduffield/lazygit.git",
			DefaultBranch: "master",
			Blueprint:     "benchmark-baseline",
			Family:        "feature-resurrection",
			Tags:          []string{"baseline", "go", "tui", "keybindings", "ui"},
			QualityGates: []string{
				"go test ./pkg/gui/... -count=1",
			},
			Baseline:      "2b783d1bc6c162ac889a85d2019276be6a63ad6f",
			FeatureRange:  "8a4506066a12b73bc0c4871bd0381bad464b948a",
			ContextPolicy: "repo-only-no-history",
			Prompt:        "Add pageUp/pageDown/top/bottom keybindings to the focused command log panel. Follow lazygit's existing keybinding and extras panel conventions, keep the behavior scoped to the command log panel, and fit the current GUI structure without unrelated keybinding changes.",
			Validation:    "go test ./pkg/gui/... -count=1",
			ExpectedPlan: &ExpectedPlan{Streams: []ExpectedStream{
				{Title: "Command log scroll mechanics"},
				{Title: "Scoped command log keybindings and panel affordances", BlockedBy: []string{"Command log scroll mechanics"}},
				{Title: "Regression coverage and keybinding reference sync", BlockedBy: []string{"Scoped command log keybindings and panel affordances"}},
			}},
		},
	}
}

func (s Spec) Readiness() string {
	if len(s.Outstanding) > 0 {
		return "draft"
	}
	return "ready"
}

func (s Spec) OutstandingText() string {
	if len(s.Outstanding) == 0 {
		return "none"
	}
	return strings.Join(s.Outstanding, "; ")
}

func (s Spec) PromptForRun() string {
	prompt := strings.TrimSpace(s.Prompt)
	if s.ExpectedPlan == nil || len(s.ExpectedPlan.Streams) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n")
	b.WriteString("Use this exact benchmark decomposition to keep hardness frozen for comparison runs:\n")
	b.WriteString("- Emit exactly these streams, in this order, with these exact titles.\n")
	b.WriteString("- Preserve the same dependency chain via `blocked_by`.\n")
	b.WriteString("- Do not collapse the proof/regression stream into docs or discoverability work.\n")
	for i, stream := range s.ExpectedPlan.Streams {
		fmt.Fprintf(&b, "%d. `%s`", i+1, stream.Title)
		if len(stream.BlockedBy) > 0 {
			fmt.Fprintf(&b, " blocked_by: %s", strings.Join(stream.BlockedBy, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (s Spec) ValidatePlanShape(streams []domain.Stream) error {
	if s.ExpectedPlan == nil || len(s.ExpectedPlan.Streams) == 0 {
		return nil
	}
	if len(streams) != len(s.ExpectedPlan.Streams) {
		return fmt.Errorf("expected %d streams, got %d", len(s.ExpectedPlan.Streams), len(streams))
	}
	for i, expected := range s.ExpectedPlan.Streams {
		actual := streams[i]
		if actual.Title != expected.Title {
			return fmt.Errorf("stream %d title mismatch: expected %q, got %q", i+1, expected.Title, actual.Title)
		}
		if !sameStrings(actual.EffectiveCard().BlockedBy, expected.BlockedBy) {
			return fmt.Errorf("stream %q blocked_by mismatch: expected %q, got %q", expected.Title, expected.BlockedBy, actual.EffectiveCard().BlockedBy)
		}
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func FindSpec(id string) (Spec, bool) {
	for _, spec := range BuiltInSpecs() {
		if spec.ID == id {
			return spec, true
		}
	}
	return Spec{}, false
}
