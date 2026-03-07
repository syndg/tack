package tools

import (
	"log/slog"
	"sort"
	"strings"
)

// Curator resolves the effective tool set for an agent.
type Curator struct {
	logger *slog.Logger
}

// NewCurator creates a new Curator with the given logger.
func NewCurator(logger *slog.Logger) *Curator {
	return &Curator{logger: logger}
}

// Curate resolves the effective tool set from all inputs.
// Resolution order:
// 1. Start with all available tools
// 2. Apply config always_exclude (remove matching)
// 3. Apply config always_include (ensure present)
// 4. Apply blueprint step include (if non-empty, filter to only matching)
// 5. Apply blueprint step exclude (remove matching)
// 6. Apply rule tool includes (union — add any matching available tools)
// 7. Apply rule tool excludes (remove matching)
// 8. Deduplicate
// 9. Cap at MaxPerAgent (keep always_include tools, drop lowest-priority extras)
func (c *Curator) Curate(input CurationInput) CurationResult {
	totalAvailable := len(input.AvailableTools)

	// Build a lookup of all available tools by name for rule include additions.
	availableByName := make(map[string]ToolSpec, len(input.AvailableTools))
	for _, t := range input.AvailableTools {
		availableByName[t.Name] = t
	}

	// 1. Start with all available tools.
	tools := make([]ToolSpec, len(input.AvailableTools))
	copy(tools, input.AvailableTools)

	// 2. Apply config always_exclude (remove matching).
	tools = filterOut(tools, input.ConfigAlways.AlwaysExclude)

	// 3. Apply config always_include (ensure present).
	tools = ensurePresent(tools, input.ConfigAlways.AlwaysInclude, availableByName)

	// 4. Apply blueprint step include (if non-empty, filter to only matching).
	if input.BlueprintTools != nil && len(input.BlueprintTools.Include) > 0 {
		tools = filterIn(tools, input.BlueprintTools.Include)
		// Re-ensure always_include tools survive blueprint filtering.
		tools = ensurePresent(tools, input.ConfigAlways.AlwaysInclude, availableByName)
	}

	// 5. Apply blueprint step exclude (remove matching).
	if input.BlueprintTools != nil && len(input.BlueprintTools.Exclude) > 0 {
		tools = filterOut(tools, input.BlueprintTools.Exclude)
	}

	// 6. Apply rule tool includes (union — add any matching available tools).
	for _, rt := range input.RuleTools {
		if len(rt.Include) > 0 {
			tools = ensurePresent(tools, rt.Include, availableByName)
		}
	}

	// 7. Apply rule tool excludes (remove matching).
	for _, rt := range input.RuleTools {
		if len(rt.Exclude) > 0 {
			tools = filterOut(tools, rt.Exclude)
		}
	}

	// 8. Deduplicate.
	tools = deduplicate(tools)

	excluded := totalAvailable - len(tools)
	if excluded < 0 {
		excluded = 0
	}

	// 9. Cap at MaxPerAgent (keep always_include tools, drop lowest-priority extras).
	capped := 0
	if input.MaxPerAgent > 0 && len(tools) > input.MaxPerAgent {
		alwaysSet := make(map[string]bool, len(input.ConfigAlways.AlwaysInclude))
		for _, pattern := range input.ConfigAlways.AlwaysInclude {
			for _, t := range tools {
				if matchGlob(pattern, t.Name) {
					alwaysSet[t.Name] = true
				}
			}
		}

		// Partition into always-keep and droppable.
		var keep, droppable []ToolSpec
		for _, t := range tools {
			if alwaysSet[t.Name] {
				keep = append(keep, t)
			} else {
				droppable = append(droppable, t)
			}
		}

		remaining := input.MaxPerAgent - len(keep)
		if remaining < 0 {
			remaining = 0
		}
		if len(droppable) > remaining {
			capped = len(droppable) - remaining
			droppable = droppable[:remaining]
		}

		tools = append(keep, droppable...)
	}

	c.logger.Debug("tool curation complete",
		"included", len(tools),
		"excluded", excluded,
		"capped", capped,
	)

	return CurationResult{
		Tools:    tools,
		Included: len(tools),
		Excluded: excluded,
		Capped:   capped,
	}
}

// matchGlob checks if a tool name matches a glob pattern.
// Supports "*" as wildcard segment: "mcp:github:*" matches "mcp:github:create_pr".
// Uses ":" as segment separator.
func matchGlob(pattern, name string) bool {
	// Exact match fast path.
	if pattern == name {
		return true
	}

	// Handle trailing wildcard: "mcp:github:*" matches any tool under "mcp:github:".
	if strings.HasSuffix(pattern, ":*") {
		prefix := pattern[:len(pattern)-1] // keep the trailing ":"
		return strings.HasPrefix(name, prefix)
	}

	// Handle bare "*" — matches everything.
	if pattern == "*" {
		return true
	}

	// Segment-by-segment matching with "*" as single-segment wildcard.
	patParts := strings.Split(pattern, ":")
	nameParts := strings.Split(name, ":")

	if len(patParts) != len(nameParts) {
		return false
	}

	for i, pp := range patParts {
		if pp == "*" {
			continue
		}
		if pp != nameParts[i] {
			return false
		}
	}

	return true
}

// filterOut removes tools whose name matches any of the given patterns.
func filterOut(tools []ToolSpec, patterns []string) []ToolSpec {
	if len(patterns) == 0 {
		return tools
	}
	result := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		excluded := false
		for _, p := range patterns {
			if matchGlob(p, t.Name) {
				excluded = true
				break
			}
		}
		if !excluded {
			result = append(result, t)
		}
	}
	return result
}

// filterIn keeps only tools whose name matches at least one of the given patterns.
func filterIn(tools []ToolSpec, patterns []string) []ToolSpec {
	if len(patterns) == 0 {
		return tools
	}
	result := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		for _, p := range patterns {
			if matchGlob(p, t.Name) {
				result = append(result, t)
				break
			}
		}
	}
	return result
}

// ensurePresent adds tools matching the given patterns from available if not already present.
func ensurePresent(tools []ToolSpec, patterns []string, available map[string]ToolSpec) []ToolSpec {
	if len(patterns) == 0 {
		return tools
	}
	present := make(map[string]bool, len(tools))
	for _, t := range tools {
		present[t.Name] = true
	}
	for _, pattern := range patterns {
		for name, spec := range available {
			if matchGlob(pattern, name) && !present[name] {
				tools = append(tools, spec)
				present[name] = true
			}
		}
	}
	return tools
}

// deduplicate removes duplicate tools by name, preserving order.
func deduplicate(tools []ToolSpec) []ToolSpec {
	seen := make(map[string]bool, len(tools))
	result := make([]ToolSpec, 0, len(tools))
	for _, t := range tools {
		if !seen[t.Name] {
			seen[t.Name] = true
			result = append(result, t)
		}
	}
	return result
}

// SortByName sorts tools alphabetically by name for deterministic output.
func SortByName(tools []ToolSpec) {
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
}
