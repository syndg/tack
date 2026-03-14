package gates

import (
	"path/filepath"
	"regexp"
	"strings"
)

// errorFilePattern matches file paths at the start of error output lines.
// Handles formats like:
//   - src/routes/expenses.ts(76,9): error TS2345
//   - src/routes/expenses.ts:76:9: error
//   - FAIL src/routes/users.test.ts > test name
//   - frontend/src/components/ExpenseForm.tsx: 45:14  warning
var errorFilePattern = regexp.MustCompile(`(?:^|\s)((?:[a-zA-Z]:)?(?:\.?\.?/)?[a-zA-Z0-9_\-./]+\.[a-zA-Z0-9]+)[\s:(]`)

// ExtractErrorFiles parses gate output (stdout+stderr combined) and
// extracts unique file paths referenced in error/warning lines.
func ExtractErrorFiles(output string) []string {
	seen := make(map[string]bool)
	var files []string

	for _, line := range strings.Split(output, "\n") {
		matches := errorFilePattern.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			f := m[1]
			// Skip common false positives
			if isLikelyNotFile(f) {
				continue
			}
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return files
}

// FilesInScope returns the subset of files that match any of the given scope patterns.
func FilesInScope(files []string, scope []string) []string {
	if len(scope) == 0 {
		return files
	}

	var matched []string
	for _, f := range files {
		if matchesAnyPattern(f, scope) {
			matched = append(matched, f)
		}
	}
	return matched
}

// FilterOutputByScope takes gate output and a file scope, and returns only
// the lines that reference files within scope. Used for fix-loop context.
func FilterOutputByScope(output string, scope []string) string {
	if len(scope) == 0 {
		return output
	}

	var filtered []string
	for _, line := range strings.Split(output, "\n") {
		matches := errorFilePattern.FindAllStringSubmatch(line, -1)
		if len(matches) == 0 {
			// Non-file lines (headers, summaries) — include them
			filtered = append(filtered, line)
			continue
		}
		for _, m := range matches {
			if matchesAnyPattern(m[1], scope) {
				filtered = append(filtered, line)
				break
			}
		}
	}
	return strings.Join(filtered, "\n")
}

func matchesAnyPattern(filePath string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(filePath, pattern) {
			return true
		}
	}
	return false
}

func matchPattern(filePath, pattern string) bool {
	// Direct match
	if filePath == pattern {
		return true
	}

	// Directory prefix: "backend/src/routes" matches "backend/src/routes/users.ts"
	if !strings.Contains(pattern, "*") {
		return strings.HasPrefix(filePath, pattern+"/") || filePath == pattern
	}

	// Recursive glob: "backend/src/**" matches anything under backend/src/
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return strings.HasPrefix(filePath, prefix+"/")
	}

	// Glob with wildcards in filename: "backend/src/routes/*.ts"
	if strings.Contains(pattern, "*") {
		matched, _ := filepath.Match(pattern, filePath)
		return matched
	}

	return false
}

func isLikelyNotFile(s string) bool {
	// Too short to be a file path
	if len(s) < 3 {
		return true
	}
	// No extension
	if !strings.Contains(s, ".") {
		return true
	}
	// URLs
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return true
	}
	// Package names like "github.com/..."
	if strings.Count(s, "/") > 0 && strings.Contains(s, ".com/") {
		return true
	}
	return false
}
