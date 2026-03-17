package merge

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/syndg/deck/internal/sandbox"
)

// MergeResult describes the outcome of a merge attempt.
type MergeResult struct {
	Success      bool     `json:"success"`
	Tier         int      `json:"tier"` // which tier resolved it (1-4)
	FilesChanged int      `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
	Conflicts    []string `json:"conflicts"` // conflicting file paths (if any)
	Error        string   `json:"error"`
}

// GitMerger performs branch merge operations inside a sandbox.
type GitMerger struct {
	logger *slog.Logger
}

// NewGitMerger creates a new GitMerger.
func NewGitMerger(logger *slog.Logger) *GitMerger {
	return &GitMerger{logger: logger}
}

// Merge attempts to merge the given branch into the current branch (main).
// Tries tiers sequentially: 1 (clean), 2 (auto-resolve), 3 (AI-resolve stub).
// Returns the result with the tier that succeeded, or failure details.
func (m *GitMerger) Merge(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error) {
	// Tier 1: clean merge
	m.logger.Info("attempting tier 1 clean merge", "branch", branch)
	result, err := m.TryCleanMerge(ctx, sb, branch)
	if err != nil {
		return nil, fmt.Errorf("tier 1 clean merge: %w", err)
	}
	if result.Success {
		return result, nil
	}

	// Tier 2: auto-resolve (favor incoming changes)
	m.logger.Info("tier 1 failed, attempting tier 2 auto-resolve",
		"branch", branch, "conflicts", result.Conflicts)
	result, err = m.TryAutoResolve(ctx, sb, branch)
	if err != nil {
		return nil, fmt.Errorf("tier 2 auto-resolve: %w", err)
	}
	if result.Success {
		return result, nil
	}

	// Tier 3: AI merge — stub for now
	m.logger.Warn("tier 2 failed, tier 3 AI merge not yet implemented",
		"branch", branch, "conflicts", result.Conflicts)
	result.Tier = 3
	result.Error = "tier 3 AI merge not yet implemented"

	return result, nil
}

// TryCleanMerge attempts a tier 1 clean merge (no conflicts).
// Runs: git merge --no-edit origin/{branch}
// Returns success=true if merge completes without conflicts.
// On conflict: runs git merge --abort and returns success=false with conflict list.
func (m *GitMerger) TryCleanMerge(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error) {
	// Fetch latest refs
	// Fetch all branches. Try full refspec first (needed for Daytona clones
	// which default to HEAD-only), fall back to plain fetch for local worktrees.
	if res, _ := sb.Exec(ctx, "git fetch origin '+refs/heads/*:refs/remotes/origin/*'", sandbox.ExecOpts{}); res.ExitCode != 0 {
		sb.Exec(ctx, "git fetch origin", sandbox.ExecOpts{})
	}

	// Use origin/ prefix — for remote sandboxes (Daytona) the branch only
	// exists on origin after push. For local worktrees, origin/ also works
	// as long as push ran (falls back to local ref if origin/ not found).
	mergeRef := "origin/" + branch
	// Check if origin ref exists; fall back to local branch name for worktrees.
	checkRes, _ := sb.Exec(ctx, fmt.Sprintf("git rev-parse --verify %s", mergeRef), sandbox.ExecOpts{})
	if checkRes.ExitCode != 0 {
		mergeRef = branch
	}
	// Record pre-merge HEAD so we can diff against it after merge.
	preMergeRef := m.getHeadRef(ctx, sb)

	cmd := fmt.Sprintf("git merge --no-edit %s", mergeRef)
	res, err := sb.Exec(ctx, cmd, sandbox.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("executing git merge: %w", err)
	}

	if res.ExitCode == 0 {
		// Clean merge succeeded — collect diff stats
		filesChanged, insertions, deletions, err := m.GetDiffStat(ctx, sb, preMergeRef)
		if err != nil {
			m.logger.Warn("failed to get diff stat after clean merge", "error", err)
		}
		return &MergeResult{
			Success:      true,
			Tier:         1,
			FilesChanged: filesChanged,
			Insertions:   insertions,
			Deletions:    deletions,
		}, nil
	}

	// Merge failed — check for conflicts
	if strings.Contains(res.Stderr, "CONFLICT") || strings.Contains(res.Stdout, "CONFLICT") {
		conflicts, err := m.GetConflictFiles(ctx, sb)
		if err != nil {
			m.logger.Warn("failed to get conflict files", "error", err)
		}
		if err := m.AbortMerge(ctx, sb); err != nil {
			m.logger.Warn("failed to abort merge", "error", err)
		}
		return &MergeResult{
			Success:   false,
			Tier:      1,
			Conflicts: conflicts,
			Error:     "merge conflicts detected",
		}, nil
	}

	// Non-conflict failure
	if err := m.AbortMerge(ctx, sb); err != nil {
		m.logger.Warn("failed to abort merge", "error", err)
	}
	return &MergeResult{
		Success: false,
		Tier:    1,
		Error:   fmt.Sprintf("merge failed: %s", strings.TrimSpace(res.Stderr)),
	}, nil
}

// TryAutoResolve attempts tier 2 auto-resolution.
// Uses git merge -X theirs to favor incoming changes — appropriate when
// streams have file scope isolation (no overlapping edits).
func (m *GitMerger) TryAutoResolve(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error) {
	// Use origin/ prefix for remote sandboxes; fall back to local ref.
	mergeRef := "origin/" + branch
	checkRes, _ := sb.Exec(ctx, fmt.Sprintf("git rev-parse --verify %s", mergeRef), sandbox.ExecOpts{})
	if checkRes.ExitCode != 0 {
		mergeRef = branch
	}
	// Record pre-merge HEAD so we can diff against it after merge.
	preMergeRef := m.getHeadRef(ctx, sb)

	cmd := fmt.Sprintf("git merge -X theirs --no-edit %s", mergeRef)
	res, err := sb.Exec(ctx, cmd, sandbox.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("executing git merge -X theirs: %w", err)
	}

	if res.ExitCode == 0 {
		// Auto-resolve succeeded — collect diff stats
		filesChanged, insertions, deletions, err := m.GetDiffStat(ctx, sb, preMergeRef)
		if err != nil {
			m.logger.Warn("failed to get diff stat after auto-resolve", "error", err)
		}
		return &MergeResult{
			Success:      true,
			Tier:         2,
			FilesChanged: filesChanged,
			Insertions:   insertions,
			Deletions:    deletions,
		}, nil
	}

	// Still failed — get conflicts and abort
	conflicts, err := m.GetConflictFiles(ctx, sb)
	if err != nil {
		m.logger.Warn("failed to get conflict files after auto-resolve", "error", err)
	}
	if err := m.AbortMerge(ctx, sb); err != nil {
		m.logger.Warn("failed to abort merge after auto-resolve", "error", err)
	}
	return &MergeResult{
		Success:   false,
		Tier:      2,
		Conflicts: conflicts,
		Error:     "auto-resolve with -X theirs failed",
	}, nil
}

// getHeadRef returns the current HEAD commit SHA (short form).
// Returns empty string on failure (caller should handle gracefully).
func (m *GitMerger) getHeadRef(ctx context.Context, sb sandbox.Sandbox) string {
	res, err := sb.Exec(ctx, "git rev-parse HEAD", sandbox.ExecOpts{})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// GetDiffStat returns diff statistics for the last merge.
// If preMergeRef is provided, diffs against it directly (most reliable).
// Otherwise falls back to ORIG_HEAD and then HEAD~1.
func (m *GitMerger) GetDiffStat(ctx context.Context, sb sandbox.Sandbox, preMergeRef string) (filesChanged, insertions, deletions int, err error) {
	var commands []string
	if preMergeRef != "" {
		commands = append(commands, fmt.Sprintf("git diff --stat %s...HEAD", preMergeRef))
	}
	commands = append(commands,
		"git diff --stat ORIG_HEAD...HEAD",
		"git diff --stat HEAD~1",
	)

	var lastErr error
	for _, cmd := range commands {
		res, execErr := sb.Exec(ctx, cmd, sandbox.ExecOpts{})
		if execErr != nil {
			lastErr = fmt.Errorf("executing %q: %w", cmd, execErr)
			continue
		}
		if res.ExitCode != 0 {
			lastErr = fmt.Errorf("%s failed: %s", cmd, strings.TrimSpace(res.Stderr))
			continue
		}
		if strings.TrimSpace(res.Stdout) == "" {
			continue
		}

		return parseDiffStatSummary(res.Stdout)
	}

	if lastErr != nil {
		return 0, 0, 0, lastErr
	}
	return 0, 0, 0, nil
}

// diffStatSummaryRe matches the summary line of git diff --stat output.
// Example: " 3 files changed, 42 insertions(+), 8 deletions(-)"
var diffStatSummaryRe = regexp.MustCompile(
	`(\d+)\s+files?\s+changed(?:,\s+(\d+)\s+insertions?\(\+\))?(?:,\s+(\d+)\s+deletions?\(-\))?`,
)

// parseDiffStatSummary extracts totals from the last line of git diff --stat output.
func parseDiffStatSummary(output string) (filesChanged, insertions, deletions int, err error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return 0, 0, 0, nil
	}

	// The summary line is the last line
	summary := lines[len(lines)-1]
	matches := diffStatSummaryRe.FindStringSubmatch(summary)
	if matches == nil {
		return 0, 0, 0, fmt.Errorf("could not parse diff stat summary: %q", summary)
	}

	filesChanged, _ = strconv.Atoi(matches[1])
	if matches[2] != "" {
		insertions, _ = strconv.Atoi(matches[2])
	}
	if matches[3] != "" {
		deletions, _ = strconv.Atoi(matches[3])
	}

	return filesChanged, insertions, deletions, nil
}

// GetConflictFiles returns the list of files with merge conflicts.
// Runs: git diff --name-only --diff-filter=U
func (m *GitMerger) GetConflictFiles(ctx context.Context, sb sandbox.Sandbox) ([]string, error) {
	res, err := sb.Exec(ctx, "git diff --name-only --diff-filter=U", sandbox.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("executing git diff --name-only: %w", err)
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// AbortMerge runs git merge --abort to clean up after a failed merge attempt.
func (m *GitMerger) AbortMerge(ctx context.Context, sb sandbox.Sandbox) error {
	res, err := sb.Exec(ctx, "git merge --abort", sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("executing git merge --abort: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("git merge --abort failed: %s", strings.TrimSpace(res.Stderr))
	}
	return nil
}
