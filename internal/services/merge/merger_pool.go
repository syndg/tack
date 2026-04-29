package merge

import (
	"context"
	"fmt"
	"log/slog"
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
// First call per objective creates a dedicated merger sandbox from the base branch.
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
// First call per objective: creates a dedicated merger sandbox from the base branch.
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
		m.logger.Warn("cached merger sandbox not found, will create a new dedicated merger sandbox",
			"sandbox_id", mergerID, "error", err)
	}

	objShort := objectiveID
	if len(objShort) > 8 {
		objShort = objShort[:8]
	}
	mergeBranch := naming.MergeBranch(objectiveID)

	sb, err := m.sandboxProv.Create(ctx, sandbox.CreateOpts{
		Name:    fmt.Sprintf("tack-%s-merge", objShort),
		Branch:  mergeBranch,
		BaseRef: m.baseBranch,
		Labels: map[string]string{
			"tack.objective": objectiveID,
			"tack.role":      "merger",
		},
		Ephemeral:   true,
		ReuseBranch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating dedicated merger sandbox: %w", err)
	}

	m.logger.Info("created dedicated merger sandbox",
		"sandbox_id", sb.ID(),
		"objective", objectiveID,
		"branch", mergeBranch,
	)

	// Track this sandbox as the merger for subsequent entries.
	m.mu.Lock()
	m.sandboxes[objectiveID] = sb.ID()
	m.mu.Unlock()

	// Persist to database so it survives daemon restarts.
	if err := m.persister.UpdateMergerSandboxID(ctx, objectiveID, sb.ID()); err != nil {
		m.logger.Warn("failed to persist merger sandbox ID", "objective", objectiveID, "error", err)
	}

	return sb, nil
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
