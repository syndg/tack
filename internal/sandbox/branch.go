package sandbox

import "fmt"

// DeriveBranchPrefix builds a branch name prefix from CreateOpts labels.
// Returns "deck/{objective[:8]}/{role}" where role defaults to "agent".
// Callers may append a suffix (e.g., UUID segment) for uniqueness.
func DeriveBranchPrefix(opts CreateOpts) string {
	objective := opts.Labels["deck.objective"]
	if len(objective) > 8 {
		objective = objective[:8]
	}
	role := opts.Labels["deck.role"]
	if role == "" {
		role = "agent"
	}
	return fmt.Sprintf("deck/%s/%s", objective, role)
}
