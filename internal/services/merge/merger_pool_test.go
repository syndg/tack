package merge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/syndg/tack/internal/naming"
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

func TestAcquire_FirstCallCreatesDedicatedMergerSandbox(t *testing.T) {
	created := &mockSandbox{id: "sb-1"}
	var gotOpts sandbox.CreateOpts
	provider := &mockSandboxProvider{
		createFn: func(_ context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
			gotOpts = opts
			return created, nil
		},
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	got, err := pool.Acquire(context.Background(), "obj-1234567890")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.ID() != "sb-1" {
		t.Fatalf("sandbox ID = %q, want %q", got.ID(), "sb-1")
	}
	if gotOpts.Name != "tack-obj-1234-merge" {
		t.Fatalf("Name = %q", gotOpts.Name)
	}
	if gotOpts.Branch != naming.MergeBranch("obj-1234567890") {
		t.Fatalf("Branch = %q, want %q", gotOpts.Branch, naming.MergeBranch("obj-1234567890"))
	}
	if gotOpts.BaseRef != "main" {
		t.Fatalf("BaseRef = %q, want main", gotOpts.BaseRef)
	}
	if gotOpts.Labels["tack.objective"] != "obj-1234567890" {
		t.Fatalf("objective label = %q", gotOpts.Labels["tack.objective"])
	}
	if gotOpts.Labels["tack.role"] != "merger" {
		t.Fatalf("role label = %q, want merger", gotOpts.Labels["tack.role"])
	}
	if !gotOpts.Ephemeral {
		t.Fatal("expected merger sandbox to be ephemeral")
	}
	if gotOpts.SkipIgnoredCopy {
		t.Fatal("expected merger sandbox to copy ignored files for dependency parity with stream sandboxes")
	}

	if id := pool.SandboxID("obj-1234567890"); id != "sb-1" {
		t.Fatalf("SandboxID = %q, want %q", id, "sb-1")
	}
	if id := persister.GetMergerSandboxID(context.Background(), "obj-1234567890"); id != "sb-1" {
		t.Fatalf("persisted ID = %q, want %q", id, "sb-1")
	}
}

func TestAcquire_SubsequentCallReturnsCachedSandbox(t *testing.T) {
	createCalls := 0
	created := &mockSandbox{id: "sb-1"}
	provider := &mockSandboxProvider{}
	provider.createFn = func(_ context.Context, _ sandbox.CreateOpts) (sandbox.Sandbox, error) {
		createCalls++
		provider.sandboxes = []sandbox.Sandbox{created}
		return created, nil
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())
	ctx := context.Background()

	first, err := pool.Acquire(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Acquire first: %v", err)
	}
	second, err := pool.Acquire(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Acquire second: %v", err)
	}
	if first.ID() != second.ID() {
		t.Fatalf("cached sandbox mismatch: %q vs %q", first.ID(), second.ID())
	}
	if createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", createCalls)
	}
}

func TestAcquire_DBRecoveryOnRestart(t *testing.T) {
	sb := &mockSandbox{id: "sb-db"}
	provider := &mockSandboxProvider{sandboxes: []sandbox.Sandbox{sb}}

	persister := newMockPersister()
	persister.data["obj-1"] = "sb-db"

	pool := NewMergerPool(provider, persister, "main", slog.Default())

	if id := pool.SandboxID("obj-1"); id != "sb-db" {
		t.Fatalf("SandboxID = %q, want %q", id, "sb-db")
	}
}

func TestAcquire_StaleSandboxCreatesNewDedicatedMergerSandbox(t *testing.T) {
	newSb := &mockSandbox{id: "sb-new"}
	createCalls := 0
	provider := &mockSandboxProvider{
		sandboxes: []sandbox.Sandbox{},
		createFn: func(_ context.Context, _ sandbox.CreateOpts) (sandbox.Sandbox, error) {
			createCalls++
			return newSb, nil
		},
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	pool.mu.Lock()
	pool.sandboxes["obj-1"] = "sb-stale"
	pool.mu.Unlock()

	got, err := pool.Acquire(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got.ID() != "sb-new" {
		t.Fatalf("sandbox ID = %q, want %q", got.ID(), "sb-new")
	}
	if createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", createCalls)
	}
}

func TestSandboxID_CacheThenDBFallback(t *testing.T) {
	persister := newMockPersister()
	persister.data["obj-db"] = "sb-from-db"

	provider := &mockSandboxProvider{}
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	pool.mu.Lock()
	pool.sandboxes["obj-cache"] = "sb-from-cache"
	pool.mu.Unlock()

	if id := pool.SandboxID("obj-cache"); id != "sb-from-cache" {
		t.Fatalf("cache hit: got %q, want %q", id, "sb-from-cache")
	}
	if id := pool.SandboxID("obj-db"); id != "sb-from-db" {
		t.Fatalf("DB fallback: got %q, want %q", id, "sb-from-db")
	}
	if id := pool.SandboxID("obj-missing"); id != "" {
		t.Fatalf("miss: got %q, want empty", id)
	}
}

func TestAcquire_CreateFailsReturnsError(t *testing.T) {
	provider := &mockSandboxProvider{
		createFn: func(_ context.Context, _ sandbox.CreateOpts) (sandbox.Sandbox, error) {
			return nil, fmt.Errorf("sandbox creation failed")
		},
	}
	persister := newMockPersister()
	pool := NewMergerPool(provider, persister, "main", slog.Default())

	if _, err := pool.Acquire(context.Background(), "obj-1"); err == nil {
		t.Fatal("expected error when create fails")
	}
}
