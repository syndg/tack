package db

import (
	"context"
	"database/sql"
	"fmt"
)

func requireProjectID(projectID string) error {
	if projectID == "" {
		return fmt.Errorf("project_id is required")
	}
	return nil
}

func defaultProjectID(ctx context.Context, db *sql.DB) (string, error) {
	var (
		projectID string
		count     int
	)
	if err := db.QueryRowContext(ctx, `SELECT id, COUNT(*) OVER() FROM projects LIMIT 1`).Scan(&projectID, &count); err != nil {
		return "", fmt.Errorf("resolving default project_id: %w", err)
	}
	if count != 1 {
		return "", fmt.Errorf("project_id is required")
	}
	return projectID, nil
}

func projectIDForObjective(ctx context.Context, db *sql.DB, objectiveID string) (string, error) {
	if objectiveID == "" {
		return "", fmt.Errorf("objective_id is required")
	}
	return lookupProjectID(ctx, db, `SELECT project_id FROM objectives WHERE id = ?`, objectiveID)
}

func projectIDForPlan(ctx context.Context, db *sql.DB, planID string) (string, error) {
	if planID == "" {
		return "", fmt.Errorf("plan_id is required")
	}
	return lookupProjectID(ctx, db, `SELECT project_id FROM plans WHERE id = ?`, planID)
}

func projectIDForStream(ctx context.Context, db *sql.DB, streamID string) (string, error) {
	if streamID == "" {
		return "", fmt.Errorf("stream_id is required")
	}
	return lookupProjectID(ctx, db, `SELECT project_id FROM streams WHERE id = ?`, streamID)
}

func lookupProjectID(ctx context.Context, db *sql.DB, query string, arg string) (string, error) {
	var projectID string
	if err := db.QueryRowContext(ctx, query, arg).Scan(&projectID); err != nil {
		return "", fmt.Errorf("resolving project_id: %w", err)
	}
	return projectID, nil
}
