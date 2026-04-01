package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const migrationSQL = `
CREATE TABLE IF NOT EXISTS objectives (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planning',
    blueprint TEXT NOT NULL DEFAULT '',
    planning_mode TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS plans (
    id TEXT PRIMARY KEY,
    objective_id TEXT NOT NULL REFERENCES objectives(id),
    status TEXT NOT NULL DEFAULT 'draft',
    quality_gates TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS streams (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES plans(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    file_scope TEXT NOT NULL DEFAULT '[]',
    dependencies TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_sessions (
    id TEXT PRIMARY KEY,
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
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    type TEXT NOT NULL,
    payload TEXT NOT NULL,
    objective TEXT NOT NULL,
    stream TEXT NOT NULL DEFAULT '',
    read INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    objective TEXT NOT NULL DEFAULT '',
    stream TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS merge_queue (
    id TEXT PRIMARY KEY,
    stream_id TEXT NOT NULL,
    plan_id TEXT NOT NULL DEFAULT '',
    objective_id TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    tier INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    diff_stat TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mail_to_unread ON mail(to_agent, read, created_at);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(type, created_at);
CREATE INDEX IF NOT EXISTS idx_events_objective ON events(objective, created_at);

CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    blueprint_name TEXT NOT NULL,
    objective_id TEXT NOT NULL,
    current_step TEXT NOT NULL,
    step_states TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'running',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_executions_objective ON executions(objective_id);

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    objective_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_objective ON runs(objective_id);
`

// RunMigrations executes all schema migrations against the database.
func RunMigrations(db *sql.DB) error {
	if _, err := db.ExecContext(context.Background(), migrationSQL); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	if err := ensureColumnExists(db, "objectives", "planning_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensuring objectives.planning_mode: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE agent_sessions SET role = 'builder' WHERE role = 'worker'`); err != nil {
		return fmt.Errorf("normalizing agent session roles: %w", err)
	}
	// Ensure execution columns for nested sub-executions
	for _, col := range []struct{ name, def string }{
		{"parent_id", "TEXT NOT NULL DEFAULT ''"},
		{"stream_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumnExists(db, "executions", col.name, col.def); err != nil {
			return fmt.Errorf("ensuring executions.%s: %w", col.name, err)
		}
	}
	// Ensure stream.execution_id for sub-execution linkage
	if err := ensureColumnExists(db, "streams", "execution_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensuring streams.execution_id: %w", err)
	}
	// Ensure mail table has new communication protocol columns
	for _, col := range []struct{ name, def string }{
		{"subject", "TEXT NOT NULL DEFAULT ''"},
		{"body", "TEXT NOT NULL DEFAULT ''"},
		{"priority", "TEXT NOT NULL DEFAULT 'normal'"},
		{"thread_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumnExists(db, "mail", col.name, col.def); err != nil {
			return fmt.Errorf("ensuring mail.%s: %w", col.name, err)
		}
	}
	// Ensure mail dedup_key column for escalation dedup
	if err := ensureColumnExists(db, "mail", "dedup_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensuring mail.dedup_key: %w", err)
	}
	// Ensure merge_queue columns added in Phase 5
	for _, col := range []struct{ name, def string }{
		{"plan_id", "TEXT NOT NULL DEFAULT ''"},
		{"objective_id", "TEXT NOT NULL DEFAULT ''"},
		{"tier", "INTEGER NOT NULL DEFAULT 0"},
		{"error", "TEXT NOT NULL DEFAULT ''"},
		{"diff_stat", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumnExists(db, "merge_queue", col.name, col.def); err != nil {
			return fmt.Errorf("ensuring merge_queue.%s: %w", col.name, err)
		}
	}
	// Persist merger sandbox ID so it survives daemon restarts.
	if err := ensureColumnExists(db, "merge_queue", "merger_sandbox_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensuring merge_queue.merger_sandbox_id: %w", err)
	}
	return nil
}

func ensureColumnExists(db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(context.Background(), fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("reading table info: %w", err)
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
			return fmt.Errorf("scanning table info: %w", err)
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating table info: %w", err)
	}

	if _, err := db.ExecContext(context.Background(), fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("adding column: %w", err)
	}
	return nil
}
