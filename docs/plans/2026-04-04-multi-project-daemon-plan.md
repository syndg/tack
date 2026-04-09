# Multi-Project Daemon Implementation Plan

**Date:** 2026-04-04  
**Status:** Completed historical implementation plan

- [x] Add a first-class `Project` domain model and project store
- [x] Add a `projects` table to the global DB schema
- [x] Rewrite project-scoped tables to require `project_id`
- [x] Add indexes and query helpers for project-scoped reads and writes
- [x] Thread `project_id` through domain types, stores, and service boundaries
- [x] Introduce a `ProjectContext` type for resolved per-project runtime state
- [x] Implement a `ProjectContextManager` that lazily loads and caches project contexts
- [x] Refactor config loading to resolve user config plus `<repo>/.tack/config.yaml` per project
- [x] Refactor blueprint loading to merge global and project-local blueprints per project context
- [x] Refactor rules loading to merge global and project-local rules per project context
- [x] Stop booting the daemon around a single `ProjectRoot`
- [x] Stop booting the daemon around a single sandbox provider instance
- [x] Bind sandbox providers to project context so each project uses its own repo root and settings
- [x] Update runtime creation paths to consume project-scoped config instead of daemon-global project state
- [x] Add daemon APIs for project registration, listing, lookup, and relink
- [x] Add CLI project targeting by cwd with `--project` override support
- [x] Update `tack init` to bootstrap `.tack/` and register the project with the daemon
- [x] Add explicit project management commands such as add, list, inspect, relink, and remove
- [x] Require objective creation to resolve and persist `project_id`
- [x] Ensure downstream plans, streams, runs, events, mail, executions, merge queue entries, and agent sessions inherit the same `project_id`
- [x] Make machine-wide views explicit and keep project-scoped queries project-filtered by default
- [x] Handle unregistered repo errors with clear recovery to `tack init` or project registration
- [x] Handle invalid project config without destabilizing the whole daemon
- [x] Handle moved repo relink flow using stable `project_id`
- [x] Decide and implement the old local DB reset behavior for the clean-break cutover
- [x] Add config-resolution tests for two repos under one daemon process
- [x] Add fresh DB initialization tests for the new multi-project schema
- [x] Add project registry and cwd auto-resolution tests
- [x] Add service isolation tests across two registered projects
- [x] Add sandbox isolation tests so one project never uses another project's repo root
- [x] Add end-to-end daemon tests for concurrent work in two repos
- [x] Run `gofmt` and `go test ./...`
