package benchmark

import "strings"

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
			Prompt:        "Add support for undoing recent plain commit and checkout actions using git reflog. Explicitly exclude pull --rebase, merge, revert, amend/reword, fixup/squash, and broader rebase flows from this benchmark slice.",
			Validation:    "go run cmd/integration_test/main.go cli undo/undo_commit reflog/checkout",
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

func FindSpec(id string) (Spec, bool) {
	for _, spec := range BuiltInSpecs() {
		if spec.ID == id {
			return spec, true
		}
	}
	return Spec{}, false
}
