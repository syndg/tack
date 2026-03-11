package agents

// RoleDefinition describes an agent role's capabilities and constraints.
type RoleDefinition struct {
	Name        string   // "planner", "lead", "builder", "reviewer", "merger"
	Description string   // human-readable role description
	MaxDepth    int      // hierarchy depth (0=planner, 1=lead, 2=execution agent)
	CanSpawn    []string // roles this role can spawn (planner→lead, lead→builder/reviewer/merger)
	Persistent  bool     // true for planner/lead, false for delegated execution agents
}

// DefaultRoles returns the built-in role definitions.
func DefaultRoles() map[string]*RoleDefinition {
	return map[string]*RoleDefinition{
		"planner": {
			Name:        "planner",
			Description: "Explores codebase and decomposes objectives into parallel work streams",
			MaxDepth:    0,
			CanSpawn:    []string{"lead"},
			Persistent:  true,
		},
		"lead": {
			Name:        "lead",
			Description: "Manages a work stream, writes specs, and coordinates builder/reviewer/scout agents",
			MaxDepth:    1,
			CanSpawn:    []string{"builder", "reviewer", "merger"},
			Persistent:  true,
		},
		"builder": {
			Name:        "builder",
			Description: "Implements code changes according to spec",
			MaxDepth:    2,
			CanSpawn:    []string{},
			Persistent:  false,
		},
		"reviewer": {
			Name:        "reviewer",
			Description: "Reviews implementation for correctness and quality",
			MaxDepth:    2,
			CanSpawn:    []string{},
			Persistent:  false,
		},
		"merger": {
			Name:        "merger",
			Description: "Resolves merge conflicts using semantic understanding",
			MaxDepth:    1,
			CanSpawn:    []string{},
			Persistent:  false,
		},
		"scout": {
			Name:        "scout",
			Description: "Explores codebase to gather context for a task",
			MaxDepth:    2,
			CanSpawn:    []string{},
			Persistent:  false,
		},
	}
}
