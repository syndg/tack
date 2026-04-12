package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/naming"
)

const defaultJanitorInterval = 5 * time.Minute

type execFunc func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Janitor prunes stale tack branches.
//
// Policy:
//   - stream branches are ephemeral and are deleted for terminal objectives
//   - merge branches are kept only while an open PR exists
//   - local git worktrees are pruned opportunistically
//
// This makes the PR head branch the only durable branch visible to humans.
type Janitor struct {
	objectives  *db.ObjectiveStore
	projectID   string
	projectRoot string
	interval    time.Duration
	logger      *slog.Logger
	exec        execFunc
}

func NewJanitor(objectives *db.ObjectiveStore, projectID string, projectRoot string, logger *slog.Logger) *Janitor {
	return &Janitor{
		objectives:  objectives,
		projectID:   projectID,
		projectRoot: projectRoot,
		interval:    defaultJanitorInterval,
		logger:      logger.With("component", "branch-janitor"),
		exec:        defaultExec,
	}
}

func defaultExec(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func (j *Janitor) Start(ctx context.Context) {
	go func() {
		j.runOnce(ctx)

		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.runOnce(ctx)
			}
		}
	}()
}

func (j *Janitor) runOnce(ctx context.Context) {
	if err := j.prune(ctx); err != nil {
		if !isExpectedJanitorShutdownError(ctx, err) {
			j.logger.Warn("branch janitor run failed", "error", err)
		}
	}
}

func (j *Janitor) prune(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if j.projectRoot == "" {
		return nil
	}

	_, _ = j.exec(ctx, j.projectRoot, "git", "worktree", "prune")

	objectives, err := j.objectives.ListByProject(ctx, j.projectID)
	if err != nil {
		return fmt.Errorf("listing objectives: %w", err)
	}

	terminalByPrefix := make(map[string]domain.ObjectiveStatus)
	activePrefixes := make(map[string]struct{})
	mergeBranches := make(map[string]struct{})
	for _, obj := range objectives {
		prefix := naming.ObjectiveShort(obj.ID)
		switch obj.Status {
		case domain.ObjectiveStatusCompleted, domain.ObjectiveStatusFailed:
			terminalByPrefix[prefix] = obj.Status
			mergeBranches[naming.MergeBranch(obj.ID)] = struct{}{}
		default:
			activePrefixes[prefix] = struct{}{}
		}
	}
	if len(terminalByPrefix) == 0 && len(activePrefixes) == 0 {
		return nil
	}

	remoteBranches, err := j.listRemoteTackBranches(ctx)
	if err != nil {
		j.logger.Warn("listing remote tack branches", "error", err)
	}
	localBranches, err := j.listLocalTackBranches(ctx)
	if err != nil {
		j.logger.Warn("listing local tack branches", "error", err)
	}
	worktrees, err := j.listTackWorktrees(ctx)
	if err != nil {
		j.logger.Warn("listing tack worktrees", "error", err)
	}

	openPRByMergeBranch := make(map[string]bool, len(mergeBranches))
	ghAvailable := j.ghAvailable(ctx)
	if ghAvailable {
		for branch := range mergeBranches {
			open, err := j.hasOpenPR(ctx, branch)
			if err != nil {
				j.logger.Warn("checking open PR for merge branch", "branch", branch, "error", err)
				continue
			}
			openPRByMergeBranch[branch] = open
		}
	}

	branches := union(remoteBranches, localBranches)
	for _, branch := range branches {
		prefix, isMerge, ok := parseTackBranch(branch)
		if !ok {
			continue
		}
		status, terminal := terminalByPrefix[prefix]

		if isMerge {
			if _, active := activePrefixes[prefix]; active {
				continue
			}
			// Only delete merge branches when we can prove no open PR is using them.
			open, known := openPRByMergeBranch[branch]
			if terminal {
				if status == domain.ObjectiveStatusCompleted {
					if !known || open {
						continue
					}
				} else if status == domain.ObjectiveStatusFailed {
					if known && open {
						continue
					}
					if !known && ghAvailable {
						continue
					}
				}
			} else {
				// Orphaned merge branch: only delete it if there is definitely no open PR.
				if !ghAvailable {
					continue
				}
				if !known {
					openPR, err := j.hasOpenPR(ctx, branch)
					if err != nil || openPR {
						continue
					}
				} else if open {
					continue
				}
			}
		} else {
			if _, active := activePrefixes[prefix]; active {
				continue
			}
			if !terminal {
				// Orphaned stream/planner branches are safe to prune only when the
				// owning objective is no longer active.
			}
		}

		if remoteBranches[branch] {
			if err := j.deleteRemoteBranch(ctx, branch); err != nil {
				j.logger.Warn("deleting remote branch", "branch", branch, "error", err)
			}
		}
		if localBranches[branch] {
			if path := worktrees[branch]; path != "" {
				if err := j.deleteWorktree(ctx, path); err != nil {
					j.logger.Warn("deleting local worktree", "branch", branch, "path", path, "error", err)
				}
			}
			if err := j.deleteLocalBranch(ctx, branch); err != nil {
				j.logger.Warn("deleting local branch", "branch", branch, "error", err)
			}
		}
	}

	_, _ = j.exec(ctx, j.projectRoot, "git", "worktree", "prune")
	return nil
}

func isExpectedJanitorShutdownError(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "database is closed")
}

func parseTackBranch(branch string) (prefix string, isMerge bool, ok bool) {
	parts := strings.Split(branch, "/")
	if len(parts) < 3 || parts[0] != "tack" {
		return "", false, false
	}
	return parts[1], parts[2] == "merge", true
}

func union(a, b map[string]bool) []string {
	all := make(map[string]struct{}, len(a)+len(b))
	for branch := range a {
		all[branch] = struct{}{}
	}
	for branch := range b {
		all[branch] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for branch := range all {
		out = append(out, branch)
	}
	sort.Strings(out)
	return out
}

func (j *Janitor) listRemoteTackBranches(ctx context.Context) (map[string]bool, error) {
	out, err := j.exec(ctx, j.projectRoot, "git", "ls-remote", "--heads", "origin", "tack/*")
	if err != nil {
		return nil, fmt.Errorf("git ls-remote: %w: %s", err, strings.TrimSpace(string(out)))
	}
	branches := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ref := strings.TrimPrefix(fields[1], "refs/heads/")
		if strings.HasPrefix(ref, "tack/") {
			branches[ref] = true
		}
	}
	return branches, nil
}

func (j *Janitor) listLocalTackBranches(ctx context.Context) (map[string]bool, error) {
	out, err := j.exec(ctx, j.projectRoot, "git", "for-each-ref", "--format=%(refname:short)", "refs/heads/tack")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w: %s", err, strings.TrimSpace(string(out)))
	}
	branches := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "tack/") {
			branches[line] = true
		}
	}
	return branches, nil
}

func (j *Janitor) listTackWorktrees(ctx context.Context) (map[string]string, error) {
	out, err := j.exec(ctx, j.projectRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	worktrees := make(map[string]string)
	var curPath, curBranch string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			curPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			curBranch = strings.TrimPrefix(line, "branch refs/heads/")
		case strings.TrimSpace(line) == "":
			if strings.HasPrefix(curBranch, "tack/") && curPath != "" && curPath != j.projectRoot {
				worktrees[curBranch] = curPath
			}
			curPath, curBranch = "", ""
		}
	}
	if strings.HasPrefix(curBranch, "tack/") && curPath != "" && curPath != j.projectRoot {
		worktrees[curBranch] = curPath
	}
	return worktrees, nil
}

func (j *Janitor) ghAvailable(ctx context.Context) bool {
	out, err := j.exec(ctx, j.projectRoot, "gh", "--version")
	return err == nil && len(out) > 0
}

func (j *Janitor) hasOpenPR(ctx context.Context, branch string) (bool, error) {
	out, err := j.exec(ctx, j.projectRoot, "gh", "pr", "list", "--state", "open", "--head", branch, "--json", "number")
	if err != nil {
		return false, fmt.Errorf("gh pr list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return false, fmt.Errorf("parsing gh pr list output: %w", err)
	}
	return len(prs) > 0, nil
}

func (j *Janitor) deleteRemoteBranch(ctx context.Context, branch string) error {
	out, err := j.exec(ctx, j.projectRoot, "git", "push", "origin", "--delete", branch)
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if strings.Contains(trimmed, "remote ref does not exist") || strings.Contains(trimmed, "unable to delete") {
			return nil
		}
		return fmt.Errorf("git push origin --delete %s: %w: %s", branch, err, trimmed)
	}
	j.logger.Info("deleted remote branch", "branch", branch)
	return nil
}

func (j *Janitor) deleteWorktree(ctx context.Context, path string) error {
	out, err := j.exec(ctx, j.projectRoot, "git", "worktree", "remove", path, "--force")
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if strings.Contains(trimmed, "is not a working tree") || strings.Contains(trimmed, "No such file or directory") {
			return nil
		}
		return fmt.Errorf("git worktree remove %s: %w: %s", path, err, trimmed)
	}
	j.logger.Info("deleted local worktree", "path", path)
	return nil
}

func (j *Janitor) deleteLocalBranch(ctx context.Context, branch string) error {
	out, err := j.exec(ctx, j.projectRoot, "git", "branch", "-D", branch)
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if strings.Contains(trimmed, "not found") || strings.Contains(trimmed, "branch '*") {
			return nil
		}
		return fmt.Errorf("git branch -D %s: %w: %s", branch, err, trimmed)
	}
	j.logger.Info("deleted local branch", "branch", branch)
	return nil
}
