# Multi-Project Daemon Design

**Date:** 2026-04-04  
**Status:** Implemented foundation / historical design note

This doc captures the design behind the current machine-wide daemon plus project-scoped execution model. For the current public explanation, also see `docs-site/content/docs/getting-started/init-and-projects.mdx`.

## Goal

Make `tack daemon` a machine-wide service that can orchestrate multiple registered projects while preserving project-local configuration inside `.tack/`.

## Product Model

Tack has two configuration scopes:

- user/global config in `~/.config/tack/`
- project config in `<repo>/.tack/`

The daemon is machine-wide, but execution is project-scoped.

Each registered project has:

- a stable `project_id`
- a `root_path`
- a display `name`
- optional git remote metadata
- registration timestamps and status

`tack init` remains the primary bootstrap path. It creates `.tack/config.yaml` when needed and registers the repo with the daemon so it becomes discoverable.

## Registration Model

Use a hybrid registration model:

- `tack init` bootstraps and registers a project
- explicit project management commands may add, list, inspect, relink, or remove projects
- CLI commands auto-target the current project by walking up from cwd and resolving the registered repo

Default UX:

- `tack plan`, `tack status`, `tack watch`, and similar commands resolve the current project automatically from cwd
- explicit override remains available via `--project`

If a user runs a project-scoped command in an unregistered repo, Tack should direct them to `tack init` or an explicit project registration command.

## Architecture

Run one machine daemon with one shared control plane and one global database.

Promote `Project` to a first-class domain model. Every stateful record belongs to a project.

Add a `ProjectContextManager` that lazily builds and caches a `ProjectContext` per `project_id`.

Each `ProjectContext` contains:

- resolved merged config for that project
- project root path
- blueprint registry for that project
- rules engine for that project
- project-specific sandbox provider binding
- any other project-scoped runtime dependencies

The daemon should not boot around a single `ProjectRoot` or a single sandbox provider. Instead, services request project context on demand using `project_id`.

## Config And Asset Resolution

Project-local assets remain inside `.tack/`.

- config: `<repo>/.tack/config.yaml`
- blueprints: `<repo>/.tack/blueprints/`
- rules: `<repo>/.tack/rules/`

Global fallback assets remain inside `~/.config/tack/`.

- config: `~/.config/tack/config.yaml`
- blueprints: `~/.config/tack/blueprints/`
- rules: `~/.config/tack/rules/`

Resolution order stays the same in spirit:

- defaults
- user/global config
- project config

Blueprints and rules are resolved per project context so project A can never leak into project B.

## Persistence Model

Keep runtime state in one machine-wide database.

Use a clean-break schema rewrite. Do not preserve the old single-project database shape.

Add a `projects` table and require `project_id` on all project-scoped records, including:

- objectives
- plans
- streams
- runs
- events
- mail
- executions
- merge queue
- agent sessions

Queries must become project-aware by default. Machine-wide views can aggregate across projects, but project-scoped views should filter by `project_id`.

Use a generated stable `project_id` as the primary identity. Paths and remotes are metadata and may change over time.

## CLI And API Model

CLI commands resolve project from cwd by default.

Expected flow:

1. walk up from cwd to find repo root
2. resolve registered project for that root
3. include `project_id` in daemon requests
4. daemon loads `ProjectContext(project_id)`
5. all downstream planning and execution uses that context

Add project-aware daemon endpoints such as:

- `GET /projects`
- `POST /projects/register`
- `POST /projects/relink`
- `GET /projects/{id}`

Existing endpoints should either accept `project_id` explicitly or infer it from the targeted entity.

## Sandbox Model

Sandbox isolation remains per project.

For local worktrees, the provider must be bound to the specific repo root for that project. The daemon should no longer hold one local provider rooted to a single repository.

For remote providers, credentials may remain global, but provider instances still need project-specific bootstrap values such as:

- repo URL
- project root metadata
- project post-create commands
- project base branch

This keeps execution and merge operations project-correct.

## Error Handling

### Unregistered Repo

If a project-scoped command is run in an unregistered repo, return a scoped error directing the user to `tack init` or explicit registration.

### Moved Repo

Because identity is a stable `project_id`, a moved repo can be relinked without breaking history. Path updates should be explicit enough to avoid accidental collisions.

### Invalid Project Config

If `<repo>/.tack/config.yaml` or other project assets are invalid, project context creation should fail for that project only. The machine daemon stays healthy.

### Stale Context Cache

Start simple: reload or invalidate project context on each new objective and on explicit project management operations. File watching can come later if needed.

## Migration Plan

Treat this as a clean-break architectural rewrite, not a compatibility migration.

1. replace the old single-project schema with a project-first schema
2. require `project_id` on all project-scoped writes from day one
3. remove single-project assumptions from services and daemon startup
4. update reads, indexes, and APIs to be project-aware

Existing local Tack runtime state may be reset or recreated as part of the change. If an old DB is present, Tack may recreate it or fail with a clear message instructing the user to reset local state.

## Testing

Focus tests on isolation and routing correctness.

1. config resolution across two repos in one daemon process
2. project registration, auto-resolution from cwd, and relink behavior
3. fresh database initialization for the new multi-project schema
4. service isolation for objectives, plans, streams, events, agents, and merge queue entries
5. sandbox isolation so one project never uses another project's repo root
6. end-to-end multi-project daemon tests with concurrent work in two repos

## Recommendation

Implement one machine-wide daemon with first-class project tenancy.

Do not model this as one visible daemon per project. Keep one control plane, one database, project-scoped contexts, and project-local `.tack/` assets.

That matches the desired product story and creates a cleaner long-term foundation than a supervisor over many project daemons.
