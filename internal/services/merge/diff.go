package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/sandbox"
)

// FileDiff represents the diff for a single file.
type FileDiff struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // added, modified, deleted, renamed
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Patch      string `json:"patch"` // unified diff content
}

// DiffSummary is the aggregate diff data for a merge.
type DiffSummary struct {
	FilesChanged int        `json:"files_changed"`
	Insertions   int        `json:"insertions"`
	Deletions    int        `json:"deletions"`
	Files        []FileDiff `json:"files"`
}

// DiffExtractor generates diff data for client consumption.
type DiffExtractor struct {
	logger *slog.Logger
}

// NewDiffExtractor creates a new DiffExtractor.
func NewDiffExtractor(logger *slog.Logger) *DiffExtractor {
	return &DiffExtractor{logger: logger}
}

// Extract generates a DiffSummary between two refs (e.g., "main" and "stream-branch").
// Runs git diff --stat, --name-status, and full diff to build structured output.
func (d *DiffExtractor) Extract(ctx context.Context, sb sandbox.Sandbox, base, head string) (*DiffSummary, error) {
	execOpts := sandbox.ExecOpts{}

	// Get diff stat for per-file insertions/deletions and totals.
	diffRange := naming.ShellQuote(base + "..." + head)
	statResult, err := sb.Exec(ctx, fmt.Sprintf("git diff --stat %s", diffRange), execOpts)
	if err != nil {
		return nil, fmt.Errorf("running git diff --stat: %w", err)
	}
	if statResult.ExitCode != 0 {
		return nil, fmt.Errorf("git diff --stat failed (exit %d): %s", statResult.ExitCode, statResult.Stderr)
	}

	// Get name-status for file status (A/M/D/R).
	nameStatusResult, err := sb.Exec(ctx, fmt.Sprintf("git diff --name-status %s", diffRange), execOpts)
	if err != nil {
		return nil, fmt.Errorf("running git diff --name-status: %w", err)
	}
	if nameStatusResult.ExitCode != 0 {
		return nil, fmt.Errorf("git diff --name-status failed (exit %d): %s", nameStatusResult.ExitCode, nameStatusResult.Stderr)
	}

	// Get numstat for accurate per-file insertion/deletion counts.
	numStatResult, numStatErr := sb.Exec(ctx, fmt.Sprintf("git diff --numstat %s", diffRange), execOpts)
	if numStatErr != nil {
		d.logger.Warn("running git diff --numstat failed, falling back to --stat bars", "base", base, "head", head, "error", numStatErr)
	}
	if numStatErr == nil && numStatResult.ExitCode != 0 {
		d.logger.Warn("git diff --numstat failed, falling back to --stat bars", "base", base, "head", head, "exit_code", numStatResult.ExitCode, "stderr", strings.TrimSpace(numStatResult.Stderr))
	}

	// Get full unified diff for patch content.
	patchResult, err := sb.Exec(ctx, fmt.Sprintf("git diff %s", diffRange), execOpts)
	if err != nil {
		return nil, fmt.Errorf("running git diff: %w", err)
	}
	if patchResult.ExitCode != 0 {
		return nil, fmt.Errorf("git diff failed (exit %d): %s", patchResult.ExitCode, patchResult.Stderr)
	}

	// Parse outputs.
	statFiles, totalInsertions, totalDeletions := ParseDiffStat(statResult.Stdout)
	statusMap := ParseNameStatus(nameStatusResult.Stdout)
	patchMap := parsePatchOutput(patchResult.Stdout)
	numStatMap := map[string]FileDiff{}
	if numStatErr == nil && numStatResult.ExitCode == 0 {
		numStatMap = parseNumStat(numStatResult.Stdout)
	}

	// Build FileDiff entries by merging stat and name-status data.
	// Use statusMap as the authoritative file list (stat summary line can be ambiguous).
	files := make([]FileDiff, 0, len(statusMap))
	for path, status := range statusMap {
		fd := FileDiff{
			Path:   path,
			Status: status,
		}
		if ns, ok := numStatMap[path]; ok {
			fd.Insertions = ns.Insertions
			fd.Deletions = ns.Deletions
		} else {
			// Fall back to --stat parsing when --numstat is unavailable.
			for _, sf := range statFiles {
				if sf.Path == path {
					fd.Insertions = sf.Insertions
					fd.Deletions = sf.Deletions
					break
				}
			}
		}
		if patch, ok := patchMap[path]; ok {
			fd.Patch = patch
		}
		files = append(files, fd)
	}

	// If statusMap was empty but statFiles has entries, fall back to stat data.
	if len(statusMap) == 0 && len(statFiles) > 0 {
		files = make([]FileDiff, len(statFiles))
		for i, sf := range statFiles {
			files[i] = FileDiff{
				Path:   sf.Path,
				Status: "modified",
			}
			if ns, ok := numStatMap[sf.Path]; ok {
				files[i].Insertions = ns.Insertions
				files[i].Deletions = ns.Deletions
			} else {
				files[i].Insertions = sf.Insertions
				files[i].Deletions = sf.Deletions
			}
			if patch, ok := patchMap[sf.Path]; ok {
				files[i].Patch = patch
			}
		}
	}

	return &DiffSummary{
		FilesChanged: len(files),
		Insertions:   totalInsertions,
		Deletions:    totalDeletions,
		Files:        files,
	}, nil
}

// ExtractJSON returns the DiffSummary as a JSON string for storage in diff_stat.
func (d *DiffExtractor) ExtractJSON(ctx context.Context, sb sandbox.Sandbox, base, head string) (string, error) {
	summary, err := d.Extract(ctx, sb, base, head)
	if err != nil {
		return "", fmt.Errorf("extracting diff: %w", err)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("marshaling diff summary: %w", err)
	}
	return string(data), nil
}

// ParseDiffStat parses the output of `git diff --stat` into structured data.
// Example line: " src/auth/token.ts | 42 +++----"
// The last line is a summary: " 3 files changed, 42 insertions(+), 8 deletions(-)"
// Returns per-file insertions/deletions and totals.
func ParseDiffStat(output string) (files []FileDiff, totalInsertions, totalDeletions int) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return nil, 0, 0
	}

	// The last line is the summary; parse file lines before it.
	for _, line := range lines[:len(lines)-1] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fd := parseStatLine(line)
		if fd.Path != "" {
			files = append(files, fd)
		}
	}

	// Parse summary line for totals.
	summary := lines[len(lines)-1]
	totalInsertions, totalDeletions = parseSummaryLine(summary)

	return files, totalInsertions, totalDeletions
}

// parseNumStat parses `git diff --numstat` output into a map keyed by path.
// Format: "<insertions>\t<deletions>\t<path>"; binary files use "-" values.
func parseNumStat(output string) map[string]FileDiff {
	result := make(map[string]FileDiff)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		insertions := 0
		deletions := 0
		if parts[0] != "-" {
			insertions, _ = strconv.Atoi(parts[0])
		}
		if parts[1] != "-" {
			deletions, _ = strconv.Atoi(parts[1])
		}

		path := parts[2]
		result[path] = FileDiff{
			Path:       path,
			Insertions: insertions,
			Deletions:  deletions,
		}
	}
	return result
}

// parseStatLine parses a single git diff --stat file line.
// Format: " path/to/file | N +++---" or " path/to/file | Bin 0 -> 1234 bytes"
func parseStatLine(line string) FileDiff {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return FileDiff{}
	}

	path := strings.TrimSpace(parts[0])
	changeInfo := strings.TrimSpace(parts[1])

	// Binary files: "Bin 0 -> 1234 bytes"
	if strings.HasPrefix(changeInfo, "Bin") {
		return FileDiff{Path: path}
	}

	// Count plus and minus signs in the change indicator.
	var insertions, deletions int
	fields := strings.Fields(changeInfo)
	if len(fields) >= 2 {
		indicator := fields[1]
		for _, ch := range indicator {
			switch ch {
			case '+':
				insertions++
			case '-':
				deletions++
			}
		}
	}
	// If len(fields) == 1: just a number with no +/- indicator (e.g., new file).
	// Insertions/deletions are ambiguous; left as zero.

	return FileDiff{
		Path:       path,
		Insertions: insertions,
		Deletions:  deletions,
	}
}

// parseSummaryLine extracts totals from the git diff --stat summary line.
// Format: " 3 files changed, 42 insertions(+), 8 deletions(-)"
func parseSummaryLine(line string) (insertions, deletions int) {
	line = strings.TrimSpace(line)

	// Look for "N insertions(+)"
	if idx := strings.Index(line, "insertion"); idx != -1 {
		// Walk backward from idx to find the number.
		numStr := extractNumberBefore(line, idx)
		if n, err := strconv.Atoi(numStr); err == nil {
			insertions = n
		}
	}

	// Look for "N deletions(-)"
	if idx := strings.Index(line, "deletion"); idx != -1 {
		numStr := extractNumberBefore(line, idx)
		if n, err := strconv.Atoi(numStr); err == nil {
			deletions = n
		}
	}

	return insertions, deletions
}

// extractNumberBefore finds the number immediately before position idx in the string.
func extractNumberBefore(s string, idx int) string {
	// Walk backward from idx, skip spaces, then collect digits.
	end := idx
	for end > 0 && s[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	if start == end {
		return ""
	}
	return s[start:end]
}

// ParseNameStatus parses `git diff --name-status` output.
// Example: "M\tsrc/auth/token.ts" or "A\tsrc/auth/jwt.ts" or "R100\told.ts\tnew.ts"
// Returns a map of path → human-readable status.
func ParseNameStatus(output string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}

		code := parts[0]
		path := parts[1]

		// Rename entries have format "R###\told\tnew"; use the new path.
		if strings.HasPrefix(code, "R") && len(parts) >= 3 {
			path = parts[2]
		}

		status := nameStatusToLabel(code)
		result[path] = status
	}

	return result
}

// nameStatusToLabel converts a git name-status code to a human label.
func nameStatusToLabel(code string) string {
	if len(code) == 0 {
		return "unknown"
	}
	switch code[0] {
	case 'A':
		return "added"
	case 'M':
		return "modified"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	default:
		return "unknown"
	}
}

// parsePatchOutput splits a unified diff into per-file patches.
// Each file section starts with "diff --git a/path b/path".
func parsePatchOutput(output string) map[string]string {
	result := make(map[string]string)
	if output == "" {
		return result
	}

	sections := strings.Split(output, "diff --git ")
	for _, section := range sections {
		if section == "" {
			continue
		}

		// First line: "a/path b/path\n..."
		firstNewline := strings.Index(section, "\n")
		if firstNewline == -1 {
			continue
		}
		header := section[:firstNewline]

		// Extract path from "a/path b/path".
		parts := strings.Fields(header)
		if len(parts) < 2 {
			continue
		}
		// Use the b/ path (destination).
		path := strings.TrimPrefix(parts[1], "b/")

		// Reconstruct the full diff section.
		result[path] = "diff --git " + section
	}

	return result
}
