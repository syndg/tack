package db

import (
	"context"
	"database/sql"
	"fmt"
)

const migrationSQL = `
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL UNIQUE,
    config_path TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS objectives (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planning',
    blueprint TEXT NOT NULL DEFAULT '',
    planning_mode TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS dossiers (
    objective_id TEXT PRIMARY KEY REFERENCES objectives(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    content TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS plans (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    objective_id TEXT NOT NULL REFERENCES objectives(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'draft',
    quality_gates TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS streams (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    plan_id TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    file_scope TEXT NOT NULL DEFAULT '[]',
    dependencies TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending',
    execution_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    objective_id TEXT NOT NULL,
    stream_id TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    sandbox_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS mail (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    priority TEXT NOT NULL DEFAULT 'normal',
    thread_id TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    dedup_key TEXT NOT NULL DEFAULT '',
    objective TEXT NOT NULL,
    stream TEXT NOT NULL DEFAULT '',
    read INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    objective TEXT NOT NULL DEFAULT '',
    stream TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS merge_queue (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    stream_id TEXT NOT NULL,
    plan_id TEXT NOT NULL DEFAULT '',
    objective_id TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    tier INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    diff_stat TEXT NOT NULL DEFAULT '',
    merger_sandbox_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    blueprint_name TEXT NOT NULL,
    objective_id TEXT NOT NULL,
    current_step TEXT NOT NULL,
    step_states TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'running',
    parent_id TEXT NOT NULL DEFAULT '',
    stream_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    objective_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS attempts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    objective_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    execution_id TEXT NOT NULL DEFAULT '',
    stream_id TEXT NOT NULL DEFAULT '',
    step_id TEXT NOT NULL DEFAULT '',
    merge_entry_id TEXT NOT NULL DEFAULT '',
    attempt_number INTEGER NOT NULL,
    max_attempts INTEGER NOT NULL,
    failure_kind TEXT NOT NULL,
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    error_summary TEXT NOT NULL DEFAULT '',
    fix_context TEXT NOT NULL DEFAULT '',
    human_guidance TEXT NOT NULL DEFAULT '',
    triggered_by_attempt_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_projects_root_path ON projects(root_path);
CREATE INDEX IF NOT EXISTS idx_objectives_project_created ON objectives(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dossiers_project_updated ON dossiers(project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_plans_project_created ON plans(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_plans_objective ON plans(objective_id);
CREATE INDEX IF NOT EXISTS idx_streams_project_plan_created ON streams(project_id, plan_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_project_created ON agent_sessions(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_objective ON agent_sessions(objective_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_project_to_unread ON mail(project_id, to_agent, read, created_at);
CREATE INDEX IF NOT EXISTS idx_mail_project_dedup ON mail(project_id, dedup_key, read);
CREATE INDEX IF NOT EXISTS idx_events_project_type ON events(project_id, type, created_at);
CREATE INDEX IF NOT EXISTS idx_events_project_objective ON events(project_id, objective, created_at);
CREATE INDEX IF NOT EXISTS idx_merge_queue_project_status ON merge_queue(project_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_merge_queue_objective ON merge_queue(objective_id, created_at);
CREATE INDEX IF NOT EXISTS idx_executions_project_objective ON executions(project_id, objective_id);
CREATE INDEX IF NOT EXISTS idx_runs_project_status ON runs(project_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_runs_objective ON runs(objective_id);
CREATE INDEX IF NOT EXISTS idx_attempts_project_objective_created ON attempts(project_id, objective_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attempts_execution_created ON attempts(execution_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attempts_stream_created ON attempts(stream_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attempts_run_created ON attempts(run_id, created_at DESC);
`

// RunMigrations executes the clean-break multi-project schema migration.
func RunMigrations(db *sql.DB) error {
	if err := maybeResetLegacySchema(db); err != nil {
		return fmt.Errorf("resetting legacy schema: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), migrationSQL); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE agent_sessions SET role = 'builder' WHERE role = 'worker'`); err != nil {
		return fmt.Errorf("normalizing agent session roles: %w", err)
	}
	return nil
}

func maybeResetLegacySchema(db *sql.DB) error {
	hasProjects, err := tableExists(db, "projects")
	if err != nil {
		return err
	}
	if !hasProjects {
		return resetSchema(db)
	}
	hasProjectID, err := columnExists(db, "objectives", "project_id")
	if err != nil {
		return err
	}
	if !hasProjectID {
		return resetSchema(db)
	}
	return nil
}

func resetSchema(db *sql.DB) error {
	for _, table := range []string{
		"attempts",
		"agent_sessions",
		"events",
		"executions",
		"mail",
		"merge_queue",
		"runs",
		"streams",
		"plans",
		"dossiers",
		"objectives",
		"projects",
	} {
		if _, err := db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
			return fmt.Errorf("dropping %s: %w", table, err)
		}
	}
	return nil
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(context.Background(), `
		SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?
	`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking table %s: %w", table, err)
	}
	return true, nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(context.Background(), fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("reading table info for %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKey); err != nil {
			return false, fmt.Errorf("scanning table info for %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterating table info for %s: %w", table, err)
	}
	return false, nil
}
