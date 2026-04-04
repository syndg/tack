package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/domain"
)

// ProjectStore persists registered projects for the machine-wide daemon.
type ProjectStore struct {
	db *sql.DB
}

// NewProjectStore creates a new ProjectStore.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{db: db}
}

// Upsert inserts or updates a project registration.
func (s *ProjectStore) Upsert(ctx context.Context, project *domain.Project) error {
	if project.ID == "" {
		project.ID = uuid.New().String()
	}
	project.RootPath = normalizeProjectPath(project.RootPath)
	project.ConfigPath = normalizeProjectPath(project.ConfigPath)
	if project.RootPath == "" {
		return fmt.Errorf("project root path is required")
	}
	if project.ConfigPath == "" {
		return fmt.Errorf("project config path is required")
	}
	if project.Name == "" {
		project.Name = filepath.Base(project.RootPath)
	}

	now := time.Now()
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects (id, name, root_path, config_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			root_path = excluded.root_path,
			config_path = excluded.config_path,
			updated_at = excluded.updated_at
	`, project.ID, project.Name, project.RootPath, project.ConfigPath, project.CreatedAt.Unix(), project.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("upserting project: %w", err)
	}
	return nil
}

// Get retrieves a project by ID.
func (s *ProjectStore) Get(ctx context.Context, id string) (*domain.Project, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, config_path, created_at, updated_at
		FROM projects WHERE id = ?
	`, id)
	return scanProject(row, id)
}

// GetByRootPath retrieves a project by its root path.
func (s *ProjectStore) GetByRootPath(ctx context.Context, rootPath string) (*domain.Project, error) {
	rootPath = normalizeProjectPath(rootPath)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, config_path, created_at, updated_at
		FROM projects WHERE root_path = ?
	`, rootPath)
	return scanProject(row, rootPath)
}

// ResolvePath finds the registered project owning the provided path.
func (s *ProjectStore) ResolvePath(ctx context.Context, path string) (*domain.Project, error) {
	path = normalizeProjectPath(path)
	projects, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(projects, func(i, j int) bool {
		return len(projects[i].RootPath) > len(projects[j].RootPath)
	})
	for _, project := range projects {
		root := normalizeProjectPath(project.RootPath)
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			p := project
			return &p, nil
		}
	}
	return nil, sql.ErrNoRows
}

// List returns all registered projects ordered by name then path.
func (s *ProjectStore) List(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, root_path, config_path, created_at, updated_at
		FROM projects ORDER BY name ASC, root_path ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		project, err := scanProject(rows, "list")
		if err != nil {
			return nil, err
		}
		projects = append(projects, *project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects: %w", err)
	}
	return projects, nil
}

// Relink updates a project's root and config path while preserving project ID.
func (s *ProjectStore) Relink(ctx context.Context, id, rootPath, configPath string) error {
	rootPath = normalizeProjectPath(rootPath)
	configPath = normalizeProjectPath(configPath)
	result, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET root_path = ?, config_path = ?, updated_at = ?
		WHERE id = ?
	`, rootPath, configPath, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("relinking project: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking relink rows: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Remove deletes a project registration.
func (s *ProjectStore) Remove(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("removing project: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking remove rows: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type projectScannable interface {
	Scan(dest ...any) error
}

func scanProject(row projectScannable, ref string) (*domain.Project, error) {
	var project domain.Project
	var createdAt int64
	var updatedAt int64
	if err := row.Scan(&project.ID, &project.Name, &project.RootPath, &project.ConfigPath, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scanning project %s: %w", ref, err)
	}
	project.CreatedAt = time.Unix(createdAt, 0)
	project.UpdatedAt = time.Unix(updatedAt, 0)
	return &project, nil
}

func normalizeProjectPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	abs, err := filepath.Abs(cleaned)
	if err == nil {
		cleaned = abs
	}
	return cleaned
}
