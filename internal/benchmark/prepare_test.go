package benchmark

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func createFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, contents string) {
		t.Helper()
		fullPath := filepath.Join(repoDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}

	run("init")
	run("config", "user.name", "Bench Tester")
	run("config", "user.email", "bench@example.com")
	write("go.mod", "module example.com/benchrepo\n\ngo 1.24\n")
	write("pkg/gui/controllers/undo.go", "package controllers\n\nfunc Undo() string { return \"ok\" }\n")
	write("pkg/gui/controllers/undo_test.go", "package controllers\n\nimport \"testing\"\n\nfunc TestUndo(t *testing.T) {\n\tif Undo() != \"ok\" {\n\t\tt.Fatal(\"unexpected undo result\")\n\t}\n}\n")
	write("pkg/commands/git_commands/reflog.go", "package git_commands\n\nfunc ReflogCommand() string { return \"ok\" }\n")
	run("add", ".")
	run("commit", "-m", "baseline")
	return repoDir, run("rev-parse", "HEAD")
}

func TestHasPassingPreflightRejectsDirtyWorkspace(t *testing.T) {
	repoDir, baseline := createFixtureRepo(t)
	workspace := filepath.Join(t.TempDir(), "prepared")
	spec, ok := FindSpec("lazygit.undo-basic-commit-checkout")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	prep, err := PrepareWorkspace(spec, PrepareOptions{Source: repoDir, Workspace: workspace, Baseline: baseline})
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	passing, err := HasPassingPreflight(spec, workspace, prep.Baseline, prep.Head)
	if err != nil {
		t.Fatalf("HasPassingPreflight before edit: %v", err)
	}
	if !passing {
		t.Fatal("expected preflight to pass before workspace edits")
	}
	if err := os.WriteFile(filepath.Join(workspace, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile DIRTY.txt: %v", err)
	}
	passing, err = HasPassingPreflight(spec, workspace, prep.Baseline, prep.Head)
	if err != nil {
		t.Fatalf("HasPassingPreflight after edit: %v", err)
	}
	if passing {
		t.Fatal("expected dirty workspace to invalidate benchmark preflight")
	}
}
