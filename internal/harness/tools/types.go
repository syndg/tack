package tools

// ToolSpec represents a tool that can be provided to an agent.
type ToolSpec struct {
	Name     string `json:"name"`     // e.g., "mcp:github:create_pr"
	Source   string `json:"source"`   // e.g., "mcp:github", "builtin"
	Category string `json:"category"` // e.g., "filesystem", "git", "database"
}

// CurationResult is the resolved tool set for a specific agent.
type CurationResult struct {
	Tools    []ToolSpec `json:"tools"`
	Included int        `json:"included"` // count of tools included
	Excluded int        `json:"excluded"` // count of tools excluded by rules
	Capped   int        `json:"capped"`   // count of tools dropped by max_per_agent cap
}

// CurationInput holds all the inputs for tool resolution.
type CurationInput struct {
	AvailableTools []ToolSpec      // all tools available in the system
	BlueprintTools *ToolScope      // tools from the current blueprint step (include/exclude)
	RuleTools      []ToolScope     // tools from matched rules
	ConfigAlways   ConfigToolScope // global always_include / always_exclude from config
	MaxPerAgent    int             // cap from config
}

// ToolScope specifies include/exclude lists for tool filtering.
type ToolScope struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// ConfigToolScope specifies global always-include and always-exclude tool lists.
type ConfigToolScope struct {
	AlwaysInclude []string
	AlwaysExclude []string
}
