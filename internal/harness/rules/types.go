package rules

import "github.com/syndg/tack/internal/harness/tools"

// Rule represents a scoped rule loaded from a markdown file with YAML frontmatter.
type Rule struct {
	Scope    string           `yaml:"scope"`              // glob pattern (e.g., "src/auth/**")
	Priority string           `yaml:"priority,omitempty"` // "high", "normal" (default: "normal")
	Tools    *tools.ToolScope `yaml:"tools,omitempty"`    // optional tool restrictions
	Body     string           `yaml:"-"`                  // markdown content after frontmatter
	Source   string           `yaml:"-"`                  // file path this rule was loaded from
}

// MatchedRule is a rule that matched a specific file path, with its source.
type MatchedRule struct {
	Rule      *Rule
	MatchedOn string // the glob pattern that matched
}
