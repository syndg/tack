package rules

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Engine loads rules from directories and matches them against file paths.
type Engine struct {
	rules  []*Rule
	mu     sync.RWMutex
	logger *slog.Logger
}

// NewEngine creates a new rules engine.
func NewEngine(logger *slog.Logger) *Engine {
	return &Engine{
		logger: logger,
	}
}

// LoadDir loads all .md files from a directory, parsing YAML frontmatter and markdown body.
// Can be called multiple times (e.g., for global + project rules).
func (e *Engine) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading rules directory %s: %w", dir, err)
	}

	var loaded []*Rule
	var errs []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("reading %s: %v", path, err))
			continue
		}

		rule, err := ParseRule(data, path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("parsing %s: %v", path, err))
			continue
		}

		loaded = append(loaded, rule)
		e.logger.Debug("loaded rule", "source", path, "scope", rule.Scope, "priority", rule.Priority)
	}

	e.mu.Lock()
	e.rules = append(e.rules, loaded...)
	e.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("errors loading rules: %s", strings.Join(errs, "; "))
	}

	return nil
}

// ParseRule parses a single rule file (frontmatter + body).
// Frontmatter is delimited by "---" lines at the start of the file.
func ParseRule(content []byte, source string) (*Rule, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}

	var rule Rule
	if err := yaml.Unmarshal(frontmatter, &rule); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	if rule.Scope == "" {
		return nil, fmt.Errorf("rule missing required 'scope' field")
	}

	if rule.Priority == "" {
		rule.Priority = "normal"
	}

	rule.Body = strings.TrimSpace(string(body))
	rule.Source = source

	return &rule, nil
}

// splitFrontmatter splits content into YAML frontmatter and markdown body.
// Frontmatter must start with "---" on the first line and end with "---".
func splitFrontmatter(content []byte) ([]byte, []byte, error) {
	trimmed := bytes.TrimLeft(content, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return nil, nil, fmt.Errorf("file does not start with frontmatter delimiter '---'")
	}

	// Find the end of the first "---" line.
	afterFirst := bytes.Index(trimmed, []byte("\n"))
	if afterFirst == -1 {
		return nil, nil, fmt.Errorf("frontmatter has no closing delimiter")
	}
	rest := trimmed[afterFirst+1:]

	// Find the closing "---".
	closingIdx := bytes.Index(rest, []byte("---"))
	if closingIdx == -1 {
		return nil, nil, fmt.Errorf("frontmatter has no closing delimiter")
	}

	fm := rest[:closingIdx]

	// Body starts after the closing "---" line.
	afterClosing := rest[closingIdx+3:]
	// Skip the rest of the closing delimiter line.
	if nlIdx := bytes.IndexByte(afterClosing, '\n'); nlIdx != -1 {
		afterClosing = afterClosing[nlIdx+1:]
	} else {
		afterClosing = nil
	}

	return fm, afterClosing, nil
}

// Match returns all rules whose scope glob matches any of the given file paths.
// Results are sorted: high-priority rules first, then by source path.
func (e *Engine) Match(filePaths []string) []MatchedRule {
	e.mu.RLock()
	rules := make([]*Rule, len(e.rules))
	copy(rules, e.rules)
	e.mu.RUnlock()

	var matched []MatchedRule
	seen := make(map[*Rule]bool)

	for _, rule := range rules {
		if seen[rule] {
			continue
		}
		for _, fp := range filePaths {
			if matchGlob(rule.Scope, fp) {
				matched = append(matched, MatchedRule{
					Rule:      rule,
					MatchedOn: rule.Scope,
				})
				seen[rule] = true
				break
			}
		}
	}

	sortMatched(matched)
	return matched
}

// MatchSingle returns all rules whose scope glob matches a single file path.
func (e *Engine) MatchSingle(filePath string) []MatchedRule {
	return e.Match([]string{filePath})
}

// All returns all loaded rules.
func (e *Engine) All() []*Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Rule, len(e.rules))
	copy(result, e.rules)
	return result
}

// BuildContext generates the combined rules text for injection into an agent overlay.
// High-priority rules are prefixed with "IMPORTANT CONSTRAINT:" header.
func (e *Engine) BuildContext(filePaths []string) string {
	matched := e.Match(filePaths)
	if len(matched) == 0 {
		return ""
	}

	var b strings.Builder
	for i, m := range matched {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		if m.Rule.Priority == "high" {
			b.WriteString("IMPORTANT CONSTRAINT:\n")
		}
		b.WriteString(m.Rule.Body)
	}

	return b.String()
}

// sortMatched sorts matched rules: high-priority first, then by source path.
func sortMatched(matched []MatchedRule) {
	sort.Slice(matched, func(i, j int) bool {
		pi := priorityRank(matched[i].Rule.Priority)
		pj := priorityRank(matched[j].Rule.Priority)
		if pi != pj {
			return pi < pj
		}
		return matched[i].Rule.Source < matched[j].Rule.Source
	})
}

// priorityRank returns a numeric rank for sorting (lower = higher priority).
func priorityRank(p string) int {
	if p == "high" {
		return 0
	}
	return 1
}

// matchGlob checks if a file path matches a glob pattern.
// Supports standard glob characters plus "**" for matching any number of path segments.
func matchGlob(pattern, name string) bool {
	// Normalize separators to forward slash for consistent matching.
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)

	return doMatchGlob(pattern, name)
}

// doMatchGlob implements recursive glob matching with ** support.
func doMatchGlob(pattern, name string) bool {
	for {
		if pattern == "" {
			return name == ""
		}

		// Handle ** (matches zero or more path segments).
		if strings.HasPrefix(pattern, "**") {
			rest := pattern[2:]

			// "**/" — consume the separator after **.
			rest = strings.TrimPrefix(rest, "/")

			// Try matching rest against every suffix of name.
			// Start with current position (** matches zero segments).
			if doMatchGlob(rest, name) {
				return true
			}
			// Try after each path separator.
			for i := 0; i < len(name); i++ {
				if name[i] == '/' {
					if doMatchGlob(rest, name[i+1:]) {
						return true
					}
				}
			}
			return false
		}

		if name == "" {
			return false
		}

		// Handle * (matches anything except /).
		if pattern[0] == '*' {
			rest := pattern[1:]
			// Try matching rest at every position in the current segment.
			for i := 0; i <= len(name); i++ {
				if i > 0 && name[i-1] == '/' {
					break
				}
				if doMatchGlob(rest, name[i:]) {
					return true
				}
			}
			return false
		}

		// Handle ? (matches any single non-/ character).
		if pattern[0] == '?' {
			if name[0] == '/' {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
			continue
		}

		// Handle character classes [abc] or [a-z].
		if pattern[0] == '[' {
			endIdx := strings.IndexByte(pattern, ']')
			if endIdx == -1 {
				return false
			}
			class := pattern[1:endIdx]
			c := name[0]
			if c == '/' {
				return false
			}
			matched := matchCharClass(class, c)
			if !matched {
				return false
			}
			pattern = pattern[endIdx+1:]
			name = name[1:]
			continue
		}

		// Literal character match.
		if pattern[0] != name[0] {
			return false
		}

		pattern = pattern[1:]
		name = name[1:]
	}
}

// matchCharClass checks if a character matches a bracket expression like [a-z] or [abc].
func matchCharClass(class string, c byte) bool {
	negate := false
	i := 0
	if len(class) > 0 && (class[0] == '!' || class[0] == '^') {
		negate = true
		i = 1
	}

	matched := false
	for i < len(class) {
		if i+2 < len(class) && class[i+1] == '-' {
			// Range: a-z
			if c >= class[i] && c <= class[i+2] {
				matched = true
			}
			i += 3
		} else {
			if c == class[i] {
				matched = true
			}
			i++
		}
	}

	if negate {
		return !matched
	}
	return matched
}
