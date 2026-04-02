package naming

import (
	"fmt"
	"strings"
)

// StreamSlug sanitizes a stream title to a URL-safe, truncated slug.
// "CORS Middleware" → "cors-middleware"
// "Health Check DB Verification" → "health-check-db-verification"
// Truncated to 30 chars, lowercase, alphanumeric + hyphens only.
// ObjectiveShort returns the first 8 characters of an objective ID.
func ObjectiveShort(objectiveID string) string {
	if len(objectiveID) > 8 {
		return objectiveID[:8]
	}
	return objectiveID
}

// MergeBranch returns the cumulative merge branch name for an objective.
func MergeBranch(objectiveID string) string {
	return fmt.Sprintf("tack/%s/merge", ObjectiveShort(objectiveID))
}

func StreamSlug(title string) string {
	s := strings.ToLower(title)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s = b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 30 {
		s = s[:30]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "stream"
	}
	return s
}
