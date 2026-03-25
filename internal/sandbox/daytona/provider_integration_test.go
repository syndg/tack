package daytona

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/sandbox"
)

// These tests hit a real Daytona API. Skip when DAYTONA_API_KEY is not set.
// Run with:  DAYTONA_API_KEY=dtn_... go test -v -run TestIntegration ./internal/sandbox/daytona/

func skipWithoutKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("DAYTONA_API_KEY")
	if key == "" {
		t.Skip("DAYTONA_API_KEY not set, skipping integration test")
	}
	return key
}

func newLiveProvider(t *testing.T) *Provider {
	t.Helper()
	key := skipWithoutKey(t)
	p, err := New(Config{APIKey: key}, nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestIntegration_CreateExecDelete(t *testing.T) {
	p := newLiveProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// --- Create ---
	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Name:      "tack-integ-test",
		Labels:    map[string]string{"tack.objective": "integ-test-obj", "tack.role": "builder"},
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Logf("created sandbox %s", sb.ID())

	// Ensure cleanup even on failure.
	defer func() {
		if err := p.Delete(context.Background(), sb.ID()); err != nil {
			t.Logf("cleanup Delete: %v", err)
		} else {
			t.Logf("deleted sandbox %s", sb.ID())
		}
	}()

	if sb.ID() == "" {
		t.Fatal("expected non-empty sandbox ID")
	}
	if sb.Status() != sandbox.SandboxStatusRunning {
		t.Errorf("Status = %q, want running", sb.Status())
	}

	// --- Exec: basic command ---
	res, err := sb.Exec(ctx, "echo hello-from-daytona", sandbox.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec basic: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("Exec basic exit = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello-from-daytona") {
		t.Errorf("Exec basic stdout = %q, want containing 'hello-from-daytona'", res.Stdout)
	}
	t.Logf("exec basic: %q", res.Stdout)

	// --- Exec: with WorkDir ---
	res, err = sb.Exec(ctx, "pwd", sandbox.ExecOpts{WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("Exec cwd: %v", err)
	}
	if !strings.Contains(res.Stdout, "/tmp") {
		t.Errorf("Exec cwd stdout = %q, want /tmp", res.Stdout)
	}
	t.Logf("exec cwd: %q", res.Stdout)

	// --- Exec: with Env ---
	res, err = sb.Exec(ctx, "echo $DECK_TEST_VAR", sandbox.ExecOpts{
		Env: map[string]string{"DECK_TEST_VAR": "it-works"},
	})
	if err != nil {
		t.Fatalf("Exec env: %v", err)
	}
	if !strings.Contains(res.Stdout, "it-works") {
		t.Errorf("Exec env stdout = %q, want containing 'it-works'", res.Stdout)
	}
	t.Logf("exec env: %q", res.Stdout)

	// --- Upload + Download ---
	testContent := []byte("tack integration test content\n")
	if err := sb.Upload(ctx, testContent, "/tmp/tack-test-file.txt"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	downloaded, err := sb.Download(ctx, "/tmp/tack-test-file.txt")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(downloaded) != string(testContent) {
		t.Errorf("Download content = %q, want %q", string(downloaded), string(testContent))
	}
	t.Logf("upload/download roundtrip OK")
}

func TestIntegration_ListSurvivesNewProvider(t *testing.T) {
	key := skipWithoutKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// --- Provider 1: create sandbox ---
	p1, err := New(Config{APIKey: key}, nil, slog.Default())
	if err != nil {
		t.Fatalf("New p1: %v", err)
	}

	sb, err := p1.Create(ctx, sandbox.CreateOpts{
		Name:      "tack-integ-list-test",
		Labels:    map[string]string{"tack.objective": "integ-list-obj", "tack.role": "merger"},
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Logf("created sandbox %s on p1", sb.ID())

	defer func() {
		// Use p1 for cleanup since it has the sandbox cached.
		if err := p1.Delete(context.Background(), sb.ID()); err != nil {
			t.Logf("cleanup Delete: %v", err)
		}
	}()

	// --- Provider 2: fresh instance, simulates restart ---
	p2, err := New(Config{APIKey: key}, nil, slog.Default())
	if err != nil {
		t.Fatalf("New p2: %v", err)
	}

	// p2 has an empty in-memory map, but List should query the API.
	results, err := p2.List(ctx, map[string]string{"tack.objective": "integ-list-obj"})
	if err != nil {
		t.Fatalf("List on p2: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected List on fresh provider to find sandbox via API, got 0")
	}

	found := false
	for _, r := range results {
		if r.ID() == sb.ID() {
			found = true
		}
	}
	if !found {
		t.Errorf("List on p2 did not return sandbox %s", sb.ID())
	}
	t.Logf("p2 List found %d sandbox(es) including %s", len(results), sb.ID())

	// --- Get should also work on p2 ---
	got, err := p2.Get(ctx, sb.ID())
	if err != nil {
		t.Fatalf("Get on p2: %v", err)
	}
	if got.ID() != sb.ID() {
		t.Errorf("Get ID = %q, want %q", got.ID(), sb.ID())
	}
	t.Logf("p2 Get OK")
}
