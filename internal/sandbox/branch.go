package sandbox

import "fmt"

// DeriveBranchPrefix builds a branch name prefix from CreateOpts labels.
// Returns "tack/{objective[:8]}/{role}" where role defaults to "agent".
// Callers may append a suffix (e.g., UUID segment) for uniqueness.
func DeriveBranchPrefix(opts CreateOpts) string {
	objective := opts.Labels["tack.objective"]
	if len(objective) > 8 {
		objective = objective[:8]
	}
	role := opts.Labels["tack.role"]
	if role == "" {
		role = "agent"
	}
	return fmt.Sprintf("tack/%s/%s", objective, role)
}
