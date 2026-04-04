package db

import (
	"context"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

const testProjectID = "test-project"

// openTestDB opens a fresh SQLite DB with migrations applied.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	projectStore := NewProjectStore(d.Conn())
	if err := projectStore.Upsert(context.Background(), &domain.Project{
		ID:         testProjectID,
		Name:       "test-project",
		RootPath:   t.TempDir(),
		ConfigPath: t.TempDir() + "/.tack/config.yaml",
	}); err != nil {
		t.Fatalf("register test project: %v", err)
	}
	return d
}

// createTestObjective inserts a minimal objective and returns it.
func createTestObjective(t *testing.T, store *ObjectiveStore, description string) *domain.Objective {
	t.Helper()
	obj := &domain.Objective{ProjectID: testProjectID, Description: description}
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	return obj
}
