package local

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/sandbox"
)

// initTestRepo creates a fresh git repository in a temp directory and makes an
// initial empty commit so that git worktree operations can be performed.
// Skips the test if git is not available.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	dir := t.TempDir()
	runGitInDir(t, dir, "init")
	runGitInDir(t, dir,
		"-c", "user.email=tack-test@example.com",
		"-c", "user.name=Deck Test",
		"commit", "--allow-empty", "-m", "initial",
	)
	return dir
}

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newTestProvider(t *testing.T, repoDir string) *Provider {
	t.Helper()
	worktreeDir := filepath.Join(repoDir, "worktrees")
	return New(repoDir, worktreeDir, slog.Default())
}

func TestCreate_CreatesGitWorktreeWithCorrectBranchName(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{
			"tack.objective": "abcdef1234567890",
			"tack.role":      "builder",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ls, ok := sb.(*LocalSandbox)
	if !ok {
		t.Fatal("Create should return *LocalSandbox")
	}

	// Branch format: tack/{objective[:8]}/{role}-{id[:8]}
	if !strings.HasPrefix(ls.branch, "tack/abcdef12/builder-") {
		t.Errorf("branch = %q, want prefix tack/abcdef12/builder-", ls.branch)
	}
	if ls.status != sandbox.SandboxStatusRunning {
		t.Errorf("status = %q, want running", ls.status)
	}
	if sb.ID() == "" {
		t.Error("expected non-empty sandbox ID")
	}
}

func TestExec_RunsCommandInWorktreeDirectory(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-exec", "tack.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := sb.Exec(ctx, "echo hello", sandbox.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "hello" {
		t.Errorf("Stdout = %q, want hello", result.Stdout)
	}
}

func TestUploadDownload_RoundTripFiles(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-upload", "tack.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	content := []byte("hello, tack!\n")
	if err := sb.Upload(ctx, content, "sub/dir/file.txt"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got, err := sb.Download(ctx, "sub/dir/file.txt")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Download content = %q, want %q", got, content)
	}
}

func TestDelete_RemovesWorktreeAndBranch(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-delete", "tack.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := sb.ID()

	if err := p.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Sandbox should no longer be accessible via Get
	if _, err := p.Get(ctx, id); err == nil {
		t.Error("expected error from Get after Delete, got nil")
	}
}

func TestList_FiltersSandboxesByLabels(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	_, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-one", "tack.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create sb1: %v", err)
	}
	_, err = p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-two", "tack.role": "reviewer"},
	})
	if err != nil {
		t.Fatalf("Create sb2: %v", err)
	}

	// Filter by objective — should return only one
	results, err := p.List(ctx, map[string]string{"tack.objective": "obj-one"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for obj-one, got %d", len(results))
	}

	// Filter by role=builder — should return only one
	byRole, err := p.List(ctx, map[string]string{"tack.role": "builder"})
	if err != nil {
		t.Fatalf("List by role: %v", err)
	}
	if len(byRole) != 1 {
		t.Errorf("expected 1 result for role=builder, got %d", len(byRole))
	}
}

func TestGet_ReturnsErrorForNonExistentSandbox(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	_, err := p.Get(ctx, "nonexistent-sandbox-id")
	if err == nil {
		t.Error("expected error for nonexistent sandbox, got nil")
	}
}

func TestExec_DoesNotInheritHostSecrets(t *testing.T) {
	repoDir := initTestRepo(t)
	p := newTestProvider(t, repoDir)
	ctx := context.Background()

	sb, err := p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"tack.objective": "obj-env", "tack.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Set a host secret that should NOT leak into the sandbox.
	t.Setenv("SUPER_SECRET_HOST_VAR", "leaked!")

	// Exec should not see the host secret.
	res, err := sb.Exec(ctx, "echo ${SUPER_SECRET_HOST_VAR:-clean}", sandbox.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "clean" {
		t.Errorf("host secret leaked: stdout = %q, want 'clean'", res.Stdout)
	}

	// But allowlisted vars (PATH, HOME) should be present.
	res, err = sb.Exec(ctx, "echo $PATH", sandbox.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec PATH: %v", err)
	}
	if res.Stdout == "" {
		t.Error("PATH should be present in sandbox env")
	}

	// Explicitly injected vars should work.
	res, err = sb.Exec(ctx, "echo $DECK_TEST", sandbox.ExecOpts{
		Env: map[string]string{"DECK_TEST": "injected"},
	})
	if err != nil {
		t.Fatalf("Exec with env: %v", err)
	}
	if res.Stdout != "injected" {
		t.Errorf("injected var: stdout = %q, want 'injected'", res.Stdout)
	}
}

// TestRediscover_RestoresFullLabelsAfterRestart simulates a daemon restart by
// creating sandboxes with one provider instance, then constructing a fresh
// provider (empty in-memory map) and calling Rediscover. Verifies that Get and
// List return sandboxes with full (un-truncated) label values, which is the
// scenario that broke when labels were parsed from the truncated branch name.
func TestRediscover_RestoresFullLabelsAfterRestart(t *testing.T) {
	repoDir := initTestRepo(t)
	ctx := context.Background()

	// --- Phase 1: create sandboxes with the "old" provider ---
	p1 := newTestProvider(t, repoDir)

	fullObjectiveID := "abcdef12-3456-7890-abcd-ef1234567890"
	sb1, err := p1.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{
			"tack.objective": fullObjectiveID,
			"tack.role":      "builder",
			"tack.stream":    "stream-001",
		},
	})
	if err != nil {
		t.Fatalf("Create builder: %v", err)
	}

	sb2, err := p1.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{
			"tack.objective": fullObjectiveID,
			"tack.role":      "merger",
		},
	})
	if err != nil {
		t.Fatalf("Create merger: %v", err)
	}

	// Sanity: the original provider can find them.
	results, _ := p1.List(ctx, map[string]string{"tack.objective": fullObjectiveID})
	if len(results) != 2 {
		t.Fatalf("pre-restart: expected 2 sandboxes, got %d", len(results))
	}

	// --- Phase 2: simulate daemon restart — new provider, empty map ---
	p2 := newTestProvider(t, repoDir)

	// Before Rediscover, the new provider knows nothing.
	results, _ = p2.List(ctx, map[string]string{"tack.objective": fullObjectiveID})
	if len(results) != 0 {
		t.Fatalf("pre-rediscover: expected 0 sandboxes, got %d", len(results))
	}

	p2.Rediscover(ctx)

	// --- Phase 3: verify Get works with original sandbox IDs ---
	got1, err := p2.Get(ctx, sb1.ID())
	if err != nil {
		t.Fatalf("Get builder after restart: %v", err)
	}
	if got1.ID() != sb1.ID() {
		t.Errorf("Get builder ID = %q, want %q", got1.ID(), sb1.ID())
	}

	got2, err := p2.Get(ctx, sb2.ID())
	if err != nil {
		t.Fatalf("Get merger after restart: %v", err)
	}
	if got2.ID() != sb2.ID() {
		t.Errorf("Get merger ID = %q, want %q", got2.ID(), sb2.ID())
	}

	// --- Phase 4: verify List with FULL objective ID matches ---
	// This is the critical assertion: the full UUID must match, not just
	// the truncated 8-char prefix that appears in the branch name.
	results, err = p2.List(ctx, map[string]string{"tack.objective": fullObjectiveID})
	if err != nil {
		t.Fatalf("List by full objective: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("List by full objective: expected 2, got %d", len(results))
	}

	// List by role should also work.
	builders, _ := p2.List(ctx, map[string]string{
		"tack.objective": fullObjectiveID,
		"tack.role":      "builder",
	})
	if len(builders) != 1 {
		t.Errorf("List builders: expected 1, got %d", len(builders))
	}

	mergers, _ := p2.List(ctx, map[string]string{
		"tack.objective": fullObjectiveID,
		"tack.role":      "merger",
	})
	if len(mergers) != 1 {
		t.Errorf("List mergers: expected 1, got %d", len(mergers))
	}

	// Extra labels (tack.stream) should also survive.
	withStream, _ := p2.List(ctx, map[string]string{"tack.stream": "stream-001"})
	if len(withStream) != 1 {
		t.Errorf("List by stream: expected 1, got %d", len(withStream))
	}

	// --- Phase 5: verify the sandbox is functional (can exec) ---
	res, err := got1.Exec(ctx, "echo alive", sandbox.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec after restart: %v", err)
	}
	if res.Stdout != "alive" {
		t.Errorf("Exec stdout = %q, want alive", res.Stdout)
	}
}
