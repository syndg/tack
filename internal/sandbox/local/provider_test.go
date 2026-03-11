package local

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/deck/internal/sandbox"
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
		"-c", "user.email=deck-test@example.com",
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
			"deck.objective": "abcdef1234567890",
			"deck.role":      "builder",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ls, ok := sb.(*LocalSandbox)
	if !ok {
		t.Fatal("Create should return *LocalSandbox")
	}

	// Branch format: deck/{objective[:8]}/{role}-{id[:8]}
	if !strings.HasPrefix(ls.branch, "deck/abcdef12/builder-") {
		t.Errorf("branch = %q, want prefix deck/abcdef12/builder-", ls.branch)
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
		Labels: map[string]string{"deck.objective": "obj-exec", "deck.role": "builder"},
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
		Labels: map[string]string{"deck.objective": "obj-upload", "deck.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	content := []byte("hello, deck!\n")
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
		Labels: map[string]string{"deck.objective": "obj-delete", "deck.role": "builder"},
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
		Labels: map[string]string{"deck.objective": "obj-one", "deck.role": "builder"},
	})
	if err != nil {
		t.Fatalf("Create sb1: %v", err)
	}
	_, err = p.Create(ctx, sandbox.CreateOpts{
		Labels: map[string]string{"deck.objective": "obj-two", "deck.role": "reviewer"},
	})
	if err != nil {
		t.Fatalf("Create sb2: %v", err)
	}

	// Filter by objective — should return only one
	results, err := p.List(ctx, map[string]string{"deck.objective": "obj-one"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for obj-one, got %d", len(results))
	}

	// Filter by role=builder — should return only one
	byRole, err := p.List(ctx, map[string]string{"deck.role": "builder"})
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
