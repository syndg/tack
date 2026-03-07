package rules

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRule_ExtractsFrontmatterAndBody(t *testing.T) {
	content := []byte(`---
scope: "src/auth/**"
priority: high
---
Always use bcrypt for password hashing.
Never store plaintext passwords.
`)

	rule, err := ParseRule(content, "test.md")
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if rule.Scope != "src/auth/**" {
		t.Errorf("scope = %q, want %q", rule.Scope, "src/auth/**")
	}
	if rule.Priority != "high" {
		t.Errorf("priority = %q, want %q", rule.Priority, "high")
	}
	if rule.Source != "test.md" {
		t.Errorf("source = %q, want %q", rule.Source, "test.md")
	}
	if !strings.Contains(rule.Body, "bcrypt") {
		t.Errorf("body = %q, want to contain 'bcrypt'", rule.Body)
	}
}

func TestParseRule_DefaultPriority(t *testing.T) {
	content := []byte(`---
scope: "**/*.go"
---
Use gofmt.
`)
	rule, err := ParseRule(content, "test.md")
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if rule.Priority != "normal" {
		t.Errorf("priority = %q, want %q", rule.Priority, "normal")
	}
}

func TestMatch_SimpleGlob(t *testing.T) {
	logger := newTestLogger()
	engine := NewEngine(logger)

	rule := &Rule{
		Scope:    "src/*.go",
		Priority: "normal",
		Body:     "Go files rule",
		Source:   "go-rule.md",
	}
	engine.rules = append(engine.rules, rule)

	matched := engine.Match([]string{"src/main.go"})
	if len(matched) != 1 {
		t.Fatalf("matched %d rules, want 1", len(matched))
	}
	if matched[0].Rule.Body != "Go files rule" {
		t.Errorf("body = %q", matched[0].Rule.Body)
	}

	// Non-matching path.
	matched = engine.Match([]string{"lib/main.go"})
	if len(matched) != 0 {
		t.Errorf("matched %d rules for non-matching path, want 0", len(matched))
	}
}

func TestMatch_RecursiveGlob(t *testing.T) {
	logger := newTestLogger()
	engine := NewEngine(logger)

	rule := &Rule{
		Scope:    "src/**/*.go",
		Priority: "normal",
		Body:     "All Go files",
		Source:   "all-go.md",
	}
	engine.rules = append(engine.rules, rule)

	tests := []struct {
		path  string
		match bool
	}{
		{"src/main.go", true},
		{"src/pkg/util.go", true},
		{"src/pkg/sub/deep.go", true},
		{"lib/main.go", false},
		{"src/readme.md", false},
	}

	for _, tt := range tests {
		matched := engine.Match([]string{tt.path})
		got := len(matched) > 0
		if got != tt.match {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.match)
		}
	}
}

func TestBuildContext_HighPriorityPrefix(t *testing.T) {
	logger := newTestLogger()
	engine := NewEngine(logger)

	engine.rules = []*Rule{
		{Scope: "**/*.go", Priority: "high", Body: "Security first", Source: "a.md"},
		{Scope: "**/*.go", Priority: "normal", Body: "Be nice", Source: "b.md"},
	}

	ctx := engine.BuildContext([]string{"src/anything.go"})
	if !strings.HasPrefix(ctx, "IMPORTANT CONSTRAINT:\nSecurity first") {
		t.Errorf("expected high-priority prefix, got: %q", ctx[:min(60, len(ctx))])
	}
	if !strings.Contains(ctx, "---") {
		t.Error("expected separator between rules")
	}
	if !strings.Contains(ctx, "Be nice") {
		t.Error("expected normal rule body")
	}
	// "Be nice" should NOT have the IMPORTANT prefix.
	parts := strings.Split(ctx, "---")
	if len(parts) < 2 {
		t.Fatal("expected at least 2 parts")
	}
	normalPart := strings.TrimSpace(parts[1])
	if strings.HasPrefix(normalPart, "IMPORTANT CONSTRAINT:") {
		t.Error("normal-priority rule should not have IMPORTANT prefix")
	}
}

func TestLoadDir_LoadsRules(t *testing.T) {
	dir := t.TempDir()

	rule1 := `---
scope: "src/**"
priority: high
---
Rule one body.
`
	rule2 := `---
scope: "tests/**"
---
Rule two body.
`
	os.WriteFile(filepath.Join(dir, "rule1.md"), []byte(rule1), 0644)
	os.WriteFile(filepath.Join(dir, "rule2.md"), []byte(rule2), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignored"), 0644)

	logger := newTestLogger()
	engine := NewEngine(logger)
	if err := engine.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	all := engine.All()
	if len(all) != 2 {
		t.Fatalf("loaded %d rules, want 2", len(all))
	}
}

func newTestLogger() *slog.Logger {
	return slog.Default()
}

