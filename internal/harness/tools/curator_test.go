package tools

import (
	"fmt"
	"log/slog"
	"testing"
)

func TestCurate_BasicIncludeExclude(t *testing.T) {
	curator := NewCurator(slog.Default())

	input := CurationInput{
		AvailableTools: []ToolSpec{
			{Name: "read_file"},
			{Name: "write_file"},
			{Name: "delete_file"},
			{Name: "run_tests"},
		},
		BlueprintTools: &ToolScope{
			Include: []string{"read_file", "write_file", "run_tests"},
			Exclude: []string{"write_file"},
		},
	}

	result := curator.Curate(input)
	names := toolNames(result.Tools)

	if contains(names, "write_file") {
		t.Error("write_file should be excluded")
	}
	if contains(names, "delete_file") {
		t.Error("delete_file should be excluded by include filter")
	}
	if !contains(names, "read_file") {
		t.Error("read_file should be included")
	}
	if !contains(names, "run_tests") {
		t.Error("run_tests should be included")
	}
}

func TestCurate_AlwaysIncludeSurvivesExclusion(t *testing.T) {
	curator := NewCurator(slog.Default())

	input := CurationInput{
		AvailableTools: []ToolSpec{
			{Name: "read_file"},
			{Name: "write_file"},
			{Name: "security_scan"},
		},
		BlueprintTools: &ToolScope{
			Include: []string{"read_file"}, // Only include read_file.
		},
		ConfigAlways: ConfigToolScope{
			AlwaysInclude: []string{"security_scan"},
		},
	}

	result := curator.Curate(input)
	names := toolNames(result.Tools)

	if !contains(names, "security_scan") {
		t.Error("security_scan should survive as always_include")
	}
	if !contains(names, "read_file") {
		t.Error("read_file should be included")
	}
	if contains(names, "write_file") {
		t.Error("write_file should be excluded by blueprint include filter")
	}
}

func TestCurate_MaxPerAgentCap(t *testing.T) {
	curator := NewCurator(slog.Default())

	tools := make([]ToolSpec, 10)
	for i := range tools {
		tools[i] = ToolSpec{Name: fmt.Sprintf("tool_%d", i)}
	}

	input := CurationInput{
		AvailableTools: tools,
		MaxPerAgent:    5,
		ConfigAlways: ConfigToolScope{
			AlwaysInclude: []string{"tool_0"},
		},
	}

	result := curator.Curate(input)
	if len(result.Tools) != 5 {
		t.Errorf("got %d tools, want 5", len(result.Tools))
	}
	if result.Capped != 5 {
		t.Errorf("capped = %d, want 5", result.Capped)
	}
	// tool_0 should always be kept.
	names := toolNames(result.Tools)
	if !contains(names, "tool_0") {
		t.Error("tool_0 (always_include) should be kept")
	}
}

func TestGlobMatching(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"mcp:github:*", "mcp:github:create_pr", true},
		{"mcp:github:*", "mcp:github:list_issues", true},
		{"mcp:github:*", "mcp:slack:send", false},
		{"*", "anything", true},
		{"read_file", "read_file", true},
		{"read_file", "write_file", false},
		{"mcp:*:create_pr", "mcp:github:create_pr", true},
		{"mcp:*:create_pr", "mcp:gitlab:create_pr", true},
		{"mcp:*:create_pr", "mcp:github:list_prs", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.name)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func toolNames(tools []ToolSpec) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
