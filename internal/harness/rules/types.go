package rules

// Rule represents a scoped rule loaded from a markdown file with YAML frontmatter.
type Rule struct {
	Scope    string     `yaml:"scope"`              // glob pattern (e.g., "src/auth/**")
	Priority string     `yaml:"priority,omitempty"` // "high", "normal" (default: "normal")
	Tools    *ToolScope `yaml:"tools,omitempty"`    // optional tool restrictions
	Body     string     `yaml:"-"`                  // markdown content after frontmatter
	Source   string     `yaml:"-"`                  // file path this rule was loaded from
}

// ToolScope restricts which tools are available when a rule matches.
type ToolScope struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// MatchedRule is a rule that matched a specific file path, with its source.
type MatchedRule struct {
	Rule      *Rule
	MatchedOn string // the glob pattern that matched
}
