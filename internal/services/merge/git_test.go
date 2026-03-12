package merge

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/sandbox"
)

// mockSandbox implements sandbox.Sandbox for testing git operations.
type mockSandbox struct {
	id     string
	execFn func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error)
}

func (m *mockSandbox) ID() string                    { return m.id }
func (m *mockSandbox) Status() sandbox.SandboxStatus { return sandbox.SandboxStatusRunning }
func (m *mockSandbox) Upload(_ context.Context, _ []byte, _ string) error {
	return nil
}
func (m *mockSandbox) Download(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (m *mockSandbox) Stop(_ context.Context) error  { return nil }
func (m *mockSandbox) Start(_ context.Context) error { return nil }
func (m *mockSandbox) Exec(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	return m.execFn(ctx, cmd, opts)
}
func (m *mockSandbox) ExecStreaming(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	return nil, fmt.Errorf("ExecStreaming not implemented in mock")
}

func TestTryCleanMerge_Success(t *testing.T) {
	callLog := []string{}
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			callLog = append(callLog, cmd)
			switch {
			case cmd == "git fetch origin":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git merge --no-edit feature-branch":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git diff --stat HEAD~1":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   " src/auth.go | 10 ++++----\n src/jwt.go  |  5 +++--\n 2 files changed, 7 insertions(+), 8 deletions(-)\n",
				}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.TryCleanMerge(context.Background(), sb, "feature-branch")
	if err != nil {
		t.Fatalf("TryCleanMerge: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Tier != 1 {
		t.Errorf("tier = %d, want 1", result.Tier)
	}
	if result.FilesChanged != 2 {
		t.Errorf("files_changed = %d, want 2", result.FilesChanged)
	}
	if result.Insertions != 7 {
		t.Errorf("insertions = %d, want 7", result.Insertions)
	}
	if result.Deletions != 8 {
		t.Errorf("deletions = %d, want 8", result.Deletions)
	}

	// Verify fetch was called before merge.
	if len(callLog) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(callLog))
	}
	if callLog[0] != "git fetch origin" {
		t.Errorf("first call = %q, want 'git fetch origin'", callLog[0])
	}
	if callLog[1] != "git merge --no-edit feature-branch" {
		t.Errorf("second call = %q, want 'git merge --no-edit feature-branch'", callLog[1])
	}
}

func TestTryCleanMerge_Conflict(t *testing.T) {
	abortCalled := false
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch {
			case cmd == "git fetch origin":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git merge --no-edit conflict-branch":
				return sandbox.ExecResult{
					ExitCode: 1,
					Stderr:   "CONFLICT (content): Merge conflict in src/auth.go\nAutomatic merge failed",
				}, nil
			case cmd == "git diff --name-only --diff-filter=U":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   "src/auth.go\nsrc/config.go\n",
				}, nil
			case cmd == "git merge --abort":
				abortCalled = true
				return sandbox.ExecResult{ExitCode: 0}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.TryCleanMerge(context.Background(), sb, "conflict-branch")
	if err != nil {
		t.Fatalf("TryCleanMerge: %v", err)
	}
	if result.Success {
		t.Error("expected failure")
	}
	if result.Tier != 1 {
		t.Errorf("tier = %d, want 1", result.Tier)
	}
	if len(result.Conflicts) != 2 {
		t.Errorf("conflicts = %v, want 2 files", result.Conflicts)
	}
	if !abortCalled {
		t.Error("expected git merge --abort to be called")
	}
}

func TestTryAutoResolve_Success(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch {
			case cmd == "git merge -X theirs --no-edit feature-branch":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git diff --stat HEAD~1":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   " src/auth.go | 5 +++++\n 1 file changed, 5 insertions(+)\n",
				}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.TryAutoResolve(context.Background(), sb, "feature-branch")
	if err != nil {
		t.Fatalf("TryAutoResolve: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Tier != 2 {
		t.Errorf("tier = %d, want 2", result.Tier)
	}
	if result.FilesChanged != 1 {
		t.Errorf("files_changed = %d, want 1", result.FilesChanged)
	}
	if result.Insertions != 5 {
		t.Errorf("insertions = %d, want 5", result.Insertions)
	}
}

func TestTryAutoResolve_Failure(t *testing.T) {
	abortCalled := false
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch {
			case cmd == "git merge -X theirs --no-edit bad-branch":
				return sandbox.ExecResult{ExitCode: 1, Stderr: "CONFLICT"}, nil
			case cmd == "git diff --name-only --diff-filter=U":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "binary.dat\n"}, nil
			case cmd == "git merge --abort":
				abortCalled = true
				return sandbox.ExecResult{ExitCode: 0}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.TryAutoResolve(context.Background(), sb, "bad-branch")
	if err != nil {
		t.Fatalf("TryAutoResolve: %v", err)
	}
	if result.Success {
		t.Error("expected failure")
	}
	if result.Tier != 2 {
		t.Errorf("tier = %d, want 2", result.Tier)
	}
	if !abortCalled {
		t.Error("expected git merge --abort to be called")
	}
}

func TestGetDiffStat(t *testing.T) {
	tests := []struct {
		name         string
		stdout       string
		wantFiles    int
		wantInsert   int
		wantDeletion int
	}{
		{
			name:         "standard output",
			stdout:       " a.go | 10 ++++------\n b.go | 3 +++\n 2 files changed, 7 insertions(+), 6 deletions(-)\n",
			wantFiles:    2,
			wantInsert:   7,
			wantDeletion: 6,
		},
		{
			name:         "insertions only",
			stdout:       " new.go | 15 +++++++++++++++\n 1 file changed, 15 insertions(+)\n",
			wantFiles:    1,
			wantInsert:   15,
			wantDeletion: 0,
		},
		{
			name:         "deletions only",
			stdout:       " old.go | 8 --------\n 1 file changed, 8 deletions(-)\n",
			wantFiles:    1,
			wantInsert:   0,
			wantDeletion: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := &mockSandbox{
				id: "test-sb",
				execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
					return sandbox.ExecResult{ExitCode: 0, Stdout: tt.stdout}, nil
				},
			}

			merger := NewGitMerger(slog.Default())
			files, ins, del, err := merger.GetDiffStat(context.Background(), sb)
			if err != nil {
				t.Fatalf("GetDiffStat: %v", err)
			}
			if files != tt.wantFiles {
				t.Errorf("files = %d, want %d", files, tt.wantFiles)
			}
			if ins != tt.wantInsert {
				t.Errorf("insertions = %d, want %d", ins, tt.wantInsert)
			}
			if del != tt.wantDeletion {
				t.Errorf("deletions = %d, want %d", del, tt.wantDeletion)
			}
		})
	}
}

func TestGetConflictFiles(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{
				ExitCode: 0,
				Stdout:   "src/auth.go\nsrc/config.go\nsrc/main.go\n",
			}, nil
		},
	}

	merger := NewGitMerger(slog.Default())
	files, err := merger.GetConflictFiles(context.Background(), sb)
	if err != nil {
		t.Fatalf("GetConflictFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("len = %d, want 3", len(files))
	}
	expected := []string{"src/auth.go", "src/config.go", "src/main.go"}
	for i, f := range files {
		if f != expected[i] {
			t.Errorf("files[%d] = %q, want %q", i, f, expected[i])
		}
	}
}

func TestGetConflictFiles_Empty(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0, Stdout: ""}, nil
		},
	}

	merger := NewGitMerger(slog.Default())
	files, err := merger.GetConflictFiles(context.Background(), sb)
	if err != nil {
		t.Fatalf("GetConflictFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("len = %d, want 0", len(files))
	}
}

func TestAbortMerge(t *testing.T) {
	cmdCalled := ""
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			cmdCalled = cmd
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}

	merger := NewGitMerger(slog.Default())
	if err := merger.AbortMerge(context.Background(), sb); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}
	if cmdCalled != "git merge --abort" {
		t.Errorf("cmd = %q, want 'git merge --abort'", cmdCalled)
	}
}

func TestMerge_TiersSequentially(t *testing.T) {
	tierAttempts := 0
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch {
			case cmd == "git fetch origin":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git merge --no-edit test-branch":
				tierAttempts++
				// Tier 1 fails with conflict.
				return sandbox.ExecResult{
					ExitCode: 1,
					Stderr:   "CONFLICT (content): Merge conflict in file.go",
				}, nil
			case cmd == "git diff --name-only --diff-filter=U":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "file.go\n"}, nil
			case cmd == "git merge --abort":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git merge -X theirs --no-edit test-branch":
				tierAttempts++
				// Tier 2 succeeds.
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git diff --stat HEAD~1":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   " file.go | 3 +++\n 1 file changed, 3 insertions(+)\n",
				}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.Merge(context.Background(), sb, "test-branch")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !result.Success {
		t.Error("expected success at tier 2")
	}
	if result.Tier != 2 {
		t.Errorf("tier = %d, want 2", result.Tier)
	}
	if tierAttempts != 2 {
		t.Errorf("tier attempts = %d, want 2 (tier 1 + tier 2)", tierAttempts)
	}
}

func TestMerge_AllTiersFail(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch {
			case cmd == "git fetch origin":
				return sandbox.ExecResult{ExitCode: 0}, nil
			case cmd == "git merge --no-edit stuck-branch":
				return sandbox.ExecResult{
					ExitCode: 1,
					Stderr:   "CONFLICT (content): Merge conflict in binary.dat",
				}, nil
			case cmd == "git merge -X theirs --no-edit stuck-branch":
				return sandbox.ExecResult{
					ExitCode: 1,
					Stderr:   "CONFLICT (binary): Merge conflict in binary.dat",
				}, nil
			case cmd == "git diff --name-only --diff-filter=U":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "binary.dat\n"}, nil
			case cmd == "git merge --abort":
				return sandbox.ExecResult{ExitCode: 0}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	merger := NewGitMerger(slog.Default())
	result, err := merger.Merge(context.Background(), sb, "stuck-branch")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if result.Success {
		t.Error("expected failure after all tiers exhausted")
	}
	if result.Tier != 3 {
		t.Errorf("tier = %d, want 3 (stub tier)", result.Tier)
	}
	if result.Error == "" {
		t.Error("expected error message for tier 3 stub")
	}
}
