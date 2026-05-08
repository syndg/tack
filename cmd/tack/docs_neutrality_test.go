package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicSetupDocsAvoidRecommendationLanguage(t *testing.T) {
	root := repoRootForTest(t)
	files := []string{
		"README.md",
		"docs-site/content/docs/getting-started/index.mdx",
		"docs-site/content/docs/getting-started/init-and-projects.mdx",
		"docs-site/content/docs/getting-started/agent-runtimes.mdx",
		"docs-site/content/docs/getting-started/configuration.mdx",
		"docs-site/content/docs/reference/cli.mdx",
	}
	banned := []string{"best-supported", "first-class", "recommended", "quick setup", "default setup"}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", file, err)
		}
		text := strings.ToLower(string(data))
		for _, phrase := range banned {
			if index := strings.Index(text, phrase); index >= 0 {
				start := max(0, index-40)
				end := min(len(text), index+len(phrase)+40)
				t.Fatalf("%s contains non-neutral phrase %q near %q", file, phrase, text[start:end])
			}
		}
	}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from %s", dir)
		}
		dir = parent
	}
}
