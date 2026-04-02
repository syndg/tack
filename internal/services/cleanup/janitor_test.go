package cleanup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

func setupJanitorTest(t *testing.T) (*Janitor, *db.ObjectiveStore, *[]string) {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := db.NewObjectiveStore(database.Conn())
	calls := []string{}
	j := NewJanitor(store, "/repo", slog.Default())
	j.exec = func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
		calls = append(calls, cmd)
		switch cmd {
		case "git worktree prune":
			return []byte(""), nil
		case "git worktree list --porcelain":
			return []byte(""), nil
		case "git ls-remote --heads origin tack/*":
			return []byte(strings.Join([]string{
				"aaa refs/heads/tack/12345678/merge",
				"bbb refs/heads/tack/12345678/stream-one",
			}, "\n")), nil
		case "git for-each-ref --format=%(refname:short) refs/heads/tack":
			return []byte("tack/12345678/merge\ntack/12345678/stream-one\n"), nil
		case "gh --version":
			return []byte("gh version 2.0.0"), nil
		case "gh pr list --state open --head tack/12345678/merge --json number":
			return []byte("[{\"number\":42}]"), nil
		case "git push origin --delete tack/12345678/stream-one":
			return []byte("deleted"), nil
		case "git branch -D tack/12345678/stream-one":
			return []byte("Deleted branch"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s", cmd)
		}
	}
	return j, store, &calls
}

func TestJanitorKeepsOpenMergeBranchAndDeletesStreamBranches(t *testing.T) {
	j, store, calls := setupJanitorTest(t)
	ctx := context.Background()

	obj := &domain.Objective{ID: "12345678-aaaa-bbbb-cccc-123456789abc", Description: "done", Status: domain.ObjectiveStatusCompleted}
	if err := store.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if err := store.UpdateStatus(ctx, obj.ID, domain.ObjectiveStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	if err := j.prune(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}

	joined := strings.Join(*calls, "\n")
	if strings.Contains(joined, "git push origin --delete tack/12345678/merge") {
		t.Fatalf("merge branch should be kept while PR is open\n%s", joined)
	}
	if !strings.Contains(joined, "git push origin --delete tack/12345678/stream-one") {
		t.Fatalf("expected remote stream branch cleanup\n%s", joined)
	}
	if !strings.Contains(joined, "git branch -D tack/12345678/stream-one") {
		t.Fatalf("expected local stream branch cleanup\n%s", joined)
	}
}

func TestJanitorDeletesMergeBranchWhenNoOpenPR(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := db.NewObjectiveStore(database.Conn())
	ctx := context.Background()
	obj := &domain.Objective{ID: "87654321-aaaa-bbbb-cccc-123456789abc", Description: "done", Status: domain.ObjectiveStatusCompleted}
	if err := store.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if err := store.UpdateStatus(ctx, obj.ID, domain.ObjectiveStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	calls := []string{}
	j := NewJanitor(store, "/repo", slog.Default())
	j.exec = func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
		calls = append(calls, cmd)
		switch cmd {
		case "git worktree prune":
			return []byte(""), nil
		case "git worktree list --porcelain":
			return []byte(""), nil
		case "git ls-remote --heads origin tack/*":
			return []byte("aaa refs/heads/tack/87654321/merge\n"), nil
		case "git for-each-ref --format=%(refname:short) refs/heads/tack":
			return []byte("tack/87654321/merge\n"), nil
		case "gh --version":
			return []byte("gh version 2.0.0"), nil
		case "gh pr list --state open --head tack/87654321/merge --json number":
			return []byte("[]"), nil
		case "git push origin --delete tack/87654321/merge":
			return []byte("deleted"), nil
		case "git branch -D tack/87654321/merge":
			return []byte("Deleted branch"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s", cmd)
		}
	}

	if err := j.prune(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}

	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "git push origin --delete tack/87654321/merge") {
		t.Fatalf("expected remote merge branch cleanup\n%s", joined)
	}
	if !strings.Contains(joined, "git branch -D tack/87654321/merge") {
		t.Fatalf("expected local merge branch cleanup\n%s", joined)
	}
}
