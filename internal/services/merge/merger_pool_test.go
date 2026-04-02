package merge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/syndg/tack/internal/sandbox"
)

// mockPersister implements MergerSandboxPersister for testing.
type mockPersister struct {
	mu   sync.Mutex
	data map[string]string // objectiveID → sandboxID
}

func newMockPersister() *mockPersister {
	return &mockPersister{data: make(map[string]string)}
}

func (m *mockPersister) UpdateMergerSandboxID(_ context.Context, objectiveID, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[objectiveID] = sandboxID
	return nil
}

func (m *mockPersister) GetMergerSandboxID(_ context.Context, objectiveID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[objectiveID]
}

func TestAcquire_FirstCallInitializesAndResets(t *testing.T) {
	sb := &mockSandbox{
		id: "sb-1",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}
	provider := &mockSandboxProvider{sandboxes: []sandbox.Sandbox{sb}}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	got, err := pool.Acquire(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.ID() != "sb-1" {
		t.Errorf("sandbox ID = %q, want %q", got.ID(), "sb-1")
	}

	// Verify cache populated.
	if id := pool.SandboxID("obj-1"); id != "sb-1" {
		t.Errorf("SandboxID = %q, want %q", id, "sb-1")
	}

	// Verify DB persisted.
	if id := persister.GetMergerSandboxID(context.Background(), "obj-1"); id != "sb-1" {
		t.Errorf("persisted ID = %q, want %q", id, "sb-1")
	}
}

func TestAcquire_SubsequentCallReturnsCached(t *testing.T) {
	execCount := 0
	sb := &mockSandbox{
		id: "sb-1",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			execCount++
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}
	provider := &mockSandboxProvider{sandboxes: []sandbox.Sandbox{sb}}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())
	ctx := context.Background()

	// First call — initializes and resets.
	pool.Acquire(ctx, "obj-1")
	firstExecCount := execCount

	// Second call — should return cached, NO reset (no exec calls).
	got, err := pool.Acquire(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Acquire (2nd): %v", err)
	}
	if got.ID() != "sb-1" {
		t.Errorf("sandbox ID = %q, want %q", got.ID(), "sb-1")
	}
	if execCount != firstExecCount {
		t.Errorf("exec called %d times after 2nd Acquire (expected 0 additional calls)", execCount-firstExecCount)
	}
}

func TestAcquire_DBRecoveryOnRestart(t *testing.T) {
	sb := &mockSandbox{
		id: "sb-db",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}
	provider := &mockSandboxProvider{sandboxes: []sandbox.Sandbox{sb}}

	// Pre-populate DB (simulates daemon restart — in-memory cache is empty).
	persister := newMockPersister()
	persister.data["obj-1"] = "sb-db"

	pool := NewMergerPool(provider, persister, "main", slog.Default())

	// SandboxID should recover from DB.
	if id := pool.SandboxID("obj-1"); id != "sb-db" {
		t.Errorf("SandboxID = %q, want %q", id, "sb-db")
	}
}

func TestAcquire_StaleSandboxRepicks(t *testing.T) {
	newSb := &mockSandbox{
		id: "sb-new",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}
	provider := &mockSandboxProvider{
		sandboxes: []sandbox.Sandbox{newSb},
	}
	persister := newMockPersister()

	pool := NewMergerPool(provider, persister, "main", slog.Default())

	// Pre-populate cache with a stale ID that the provider can't find.
	pool.mu.Lock()
	pool.sandboxes["obj-1"] = "sb-stale"
	pool.mu.Unlock()

	got, err := pool.Acquire(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.ID() != "sb-new" {
		t.Errorf("sandbox ID = %q, want %q (should have re-picked)", got.ID(), "sb-new")
	}
}

func TestAcquire_CreateFallbackWhenNoSandboxes(t *testing.T) {
	created := &mockSandbox{
		id: "sb-created",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}
	provider := &mockSandboxProvider{
		sandboxes: nil, // List returns empty
		createFn: func(_ context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
			return created, nil
		},
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	got, err := pool.Acquire(context.Background(), "obj-12345678abcd")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.ID() != "sb-created" {
		t.Errorf("sandbox ID = %q, want %q", got.ID(), "sb-created")
	}
}

func TestSandboxID_CacheThenDBFallback(t *testing.T) {
	persister := newMockPersister()
	persister.data["obj-db"] = "sb-from-db"

	provider := &mockSandboxProvider{}
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	// Cache hit.
	pool.mu.Lock()
	pool.sandboxes["obj-cache"] = "sb-from-cache"
	pool.mu.Unlock()

	if id := pool.SandboxID("obj-cache"); id != "sb-from-cache" {
		t.Errorf("cache hit: got %q, want %q", id, "sb-from-cache")
	}

	// DB fallback.
	if id := pool.SandboxID("obj-db"); id != "sb-from-db" {
		t.Errorf("DB fallback: got %q, want %q", id, "sb-from-db")
	}

	// Miss (neither cache nor DB).
	if id := pool.SandboxID("obj-missing"); id != "" {
		t.Errorf("miss: got %q, want empty", id)
	}
}

func TestAcquire_ResetsSandboxAndFallsBackToOriginHEAD(t *testing.T) {
	var commands []string
	sb := &mockSandbox{
		id: "sb-1",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			commands = append(commands, cmd)
			switch {
			case cmd == "git reset --hard HEAD":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "rm -rf .tack-ext":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case strings.HasPrefix(cmd, "git fetch origin"):
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git rev-parse --verify origin/main^{commit}":
				return sandbox.ExecResult{ExitCode: 1}, nil
			case cmd == "git rev-parse --verify refs/remotes/origin/main^{commit}":
				return sandbox.ExecResult{ExitCode: 1}, nil
			case cmd == "git rev-parse --verify main^{commit}":
				return sandbox.ExecResult{ExitCode: 1}, nil
			case cmd == "git rev-parse --verify origin/HEAD^{commit}":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "abc123\n"}, nil
			case strings.HasPrefix(cmd, "git checkout -B tack/obj-1/merge origin/HEAD"):
				return sandbox.ExecResult{ExitCode: 0}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}
	provider := &mockSandboxProvider{sandboxes: []sandbox.Sandbox{sb}}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	if _, err := pool.Acquire(context.Background(), "obj-1"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if len(commands) == 0 || commands[0] != "git reset --hard HEAD" {
		t.Fatalf("expected first command to reset sandbox, got %v", commands)
	}
	foundCheckout := false
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, "git checkout -B tack/obj-1/merge origin/HEAD") {
			foundCheckout = true
			break
		}
	}
	if !foundCheckout {
		t.Fatalf("expected checkout from origin/HEAD, commands=%v", commands)
	}
}

func TestAcquire_CreateFailsReturnsError(t *testing.T) {
	provider := &mockSandboxProvider{
		sandboxes: nil,
		createFn: func(_ context.Context, _ sandbox.CreateOpts) (sandbox.Sandbox, error) {
			return nil, fmt.Errorf("sandbox creation failed")
		},
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	_, err := pool.Acquire(context.Background(), "obj-1")
	if err == nil {
		t.Fatal("expected error when create fails")
	}
}
