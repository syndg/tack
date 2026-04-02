package merge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/sandbox"
)

// MergerSandboxPersister persists the merger sandbox ID for an objective
// so it survives daemon restarts.
type MergerSandboxPersister interface {
	UpdateMergerSandboxID(ctx context.Context, objectiveID, sandboxID string) error
	GetMergerSandboxID(ctx context.Context, objectiveID string) string
}

// MergerPool manages merger sandboxes for objectives.
// First call per objective picks a sandbox and resets to base branch.
// Subsequent calls return the same sandbox without reset (cumulative merges).
type MergerPool struct {
	sandboxProv sandbox.SandboxProvider
	persister   MergerSandboxPersister
	baseBranch  string
	logger      *slog.Logger

	mu        sync.Mutex
	sandboxes map[string]string // objectiveID → sandboxID
}

// NewMergerPool creates a new MergerPool.
func NewMergerPool(
	sandboxProv sandbox.SandboxProvider,
	persister MergerSandboxPersister,
	baseBranch string,
	logger *slog.Logger,
) *MergerPool {
	return &MergerPool{
		sandboxProv: sandboxProv,
		persister:   persister,
		baseBranch:  baseBranch,
		logger:      logger,
		sandboxes:   make(map[string]string),
	}
}

// Acquire returns a sandbox ready for merging into the base branch.
// First call per objective: picks a sandbox, resets to base branch.
// Subsequent calls: returns the same sandbox (cumulative merge state).
func (m *MergerPool) Acquire(ctx context.Context, objectiveID string) (sandbox.Sandbox, error) {
	// Check if we already initialized a merger sandbox for this objective.
	m.mu.Lock()
	mergerID := m.sandboxes[objectiveID]
	m.mu.Unlock()

	if mergerID != "" {
		sb, err := m.sandboxProv.Get(ctx, mergerID)
		if err == nil {
			// Already initialized — return as-is (cumulative merge state).
			return sb, nil
		}
		m.logger.Warn("cached merger sandbox not found, will pick new one",
			"sandbox_id", mergerID, "error", err)
	}

	// First merge for this objective — pick any stream sandbox to reuse.
	allSandboxes, listErr := m.sandboxProv.List(ctx, map[string]string{
		"tack.objective": objectiveID,
	})

	var sb sandbox.Sandbox
	var err error
	if listErr == nil && len(allSandboxes) > 0 {
		sb = allSandboxes[0]
		m.logger.Info("reusing stream sandbox for merge", "sandbox_id", sb.ID(), "objective", objectiveID)
	} else {
		// No sandboxes available — create a new one (shouldn't happen normally).
		objShort := objectiveID
		if len(objShort) > 8 {
			objShort = objShort[:8]
		}
		sb, err = m.sandboxProv.Create(ctx, sandbox.CreateOpts{
			Name:   fmt.Sprintf("tack-%s-merger", objShort),
			Branch: fmt.Sprintf("tack/%s/merger", objShort),
			Labels: map[string]string{
				"tack.objective": objectiveID,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("creating merger sandbox: %w", err)
		}
	}

	// Track this sandbox as the merger for subsequent entries.
	m.mu.Lock()
	m.sandboxes[objectiveID] = sb.ID()
	m.mu.Unlock()

	// Persist to database so it survives daemon restarts.
	if err := m.persister.UpdateMergerSandboxID(ctx, objectiveID, sb.ID()); err != nil {
		m.logger.Warn("failed to persist merger sandbox ID", "objective", objectiveID, "error", err)
	}

	// Reset to base branch for a clean merge target (only on first entry).
	// We create a dedicated merge branch because local worktrees can't checkout
	// the base branch directly (it's already in use by the main worktree).
	// Commands are separate because Daytona ExecuteCommand doesn't support && chains.
	objShort := objectiveID
	if len(objShort) > 8 {
		objShort = objShort[:8]
	}
	mergeBranch := naming.MergeBranch(objectiveID)

	if err := m.prepareSandboxForMerge(ctx, sb); err != nil {
		return nil, err
	}

	// Fetch all branches. Try full refspec first (needed for Daytona clones
	// which default to HEAD-only), fall back to plain fetch for local worktrees.
	if res, _ := sb.Exec(ctx, "git fetch origin '+refs/heads/*:refs/remotes/origin/*'", sandbox.ExecOpts{}); res.ExitCode != 0 {
		sb.Exec(ctx, "git fetch origin", sandbox.ExecOpts{})
	}

	baseRef, err := m.resolveBaseRef(ctx, sb)
	if err != nil {
		return nil, err
	}
	if res, err := sb.Exec(ctx, fmt.Sprintf("git checkout -B %s %s", mergeBranch, baseRef), sandbox.ExecOpts{}); err != nil || res.ExitCode != 0 {
		status := ""
		if st, stErr := sb.Exec(ctx, "git status --short", sandbox.ExecOpts{}); stErr == nil {
			status = st.Stdout
		}
		return nil, fmt.Errorf("creating merge branch from %s (resolved %s): exit=%d stderr=%s status=%s", m.baseBranch, baseRef, res.ExitCode, strings.TrimSpace(res.Stderr), strings.TrimSpace(status))
	}

	return sb, nil
}

func (m *MergerPool) prepareSandboxForMerge(ctx context.Context, sb sandbox.Sandbox) error {
	if res, err := sb.Exec(ctx, "git reset --hard HEAD", sandbox.ExecOpts{}); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("resetting merger sandbox: exit=%d stderr=%s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	_, _ = sb.Exec(ctx, "rm -rf .tack-ext", sandbox.ExecOpts{})
	return nil
}

func (m *MergerPool) resolveBaseRef(ctx context.Context, sb sandbox.Sandbox) (string, error) {
	candidates := []string{
		"origin/" + m.baseBranch,
		"refs/remotes/origin/" + m.baseBranch,
		m.baseBranch,
		"origin/HEAD",
		"refs/remotes/origin/HEAD",
	}
	for _, candidate := range candidates {
		res, err := sb.Exec(ctx, fmt.Sprintf("git rev-parse --verify %s^{commit}", candidate), sandbox.ExecOpts{})
		if err == nil && res.ExitCode == 0 {
			return candidate, nil
		}
	}
	branchOut, _ := sb.Exec(ctx, "git branch -a", sandbox.ExecOpts{})
	return "", fmt.Errorf("resolving merge base ref for %s failed; branches=%s", m.baseBranch, strings.TrimSpace(branchOut.Stdout))
}

// SandboxID returns the merger sandbox ID for an objective.
// Checks in-memory cache first, then DB (daemon restart recovery).
func (m *MergerPool) SandboxID(objectiveID string) string {
	m.mu.Lock()
	id := m.sandboxes[objectiveID]
	m.mu.Unlock()
	if id != "" {
		return id
	}
	// Recover from database (daemon restart case).
	if dbID := m.persister.GetMergerSandboxID(context.Background(), objectiveID); dbID != "" {
		m.mu.Lock()
		m.sandboxes[objectiveID] = dbID
		m.mu.Unlock()
		return dbID
	}
	return ""
}
