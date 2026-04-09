# Current Architecture

This is a code-oriented map of Tack as it exists now.

For the public product story, start with `docs-site/content/docs/`. For implementation truth, use the code paths referenced here.

## Current Shape

Tack is a machine-wide daemon with project-scoped execution.

- The daemon boots shared infrastructure once: DB, event bus, credential store, observability recorder, mail broker, and HTTP routes.
- Each registered repository gets a lazily loaded `ProjectContext` with its own config, blueprints, rules, sandbox provider, planning service, merge processor, and runs service.
- The core execution model is objective -> plan -> approval -> per-stream execution -> merge -> PR.

This maps directly to the code:

- daemon bootstrap: `internal/daemon/daemon.go`
- per-project runtime assembly: `internal/daemon/project_context.go`
- CLI surface: `cmd/tack/`

## Main Runtime Path

### 1. Daemon and Project Resolution

`tack daemon` starts the machine-wide service. The daemon stores machine-wide state in one DB and resolves the active project by registration plus cwd targeting.

Key code:

- `internal/daemon/daemon.go`
- `internal/daemon/projects.go`
- `cmd/tack/project.go`
- `cmd/tack/init.go`

### 2. Config, Blueprints, Rules, and Tools

Project behavior is loaded from two config layers: user config plus `<repo>/.tack/config.yaml`. Blueprints are loaded into a registry by blueprint `id`, rules are path-scoped, and tool shaping lives under the harness layer.

Key code:

- config loading: `internal/config/config.go`
- blueprint engine and registry: `internal/harness/blueprint/`
- scoped rules: `internal/harness/rules/`
- quality gates: `internal/harness/gates/`
- tool curation: `internal/harness/tools/`

### 3. Planning and Objective Lifecycle

Objectives and plans are persisted in the DB. Planning currently lives in the planner service and produces streams with scopes and dependencies. The future discovery/dossier layer described in the docs site is not implemented yet as a separate first-class stage.

Key code:

- planning service: `internal/services/planner/`
- lifecycle manager: `internal/services/lifecycle/manager.go`
- objective, plan, and stream routes: `internal/daemon/routes_objectives.go`, `internal/daemon/routes_plans.go`

### 4. Execution, Recovery, and Scheduling

Approved work is driven by the runs and dispatch services. The coordinator advances blueprint executions, schedules streams, spawns agents, applies retry policy, and records recovery attempts.

Key code:

- run orchestration: `internal/services/runs/`
- execution coordinator and step handling: `internal/services/dispatch/coordinator.go`, `internal/services/dispatch/handlers.go`
- scheduler and spawner: `internal/services/dispatch/scheduler.go`, `internal/services/dispatch/spawner.go`
- retry policy and recovery helpers: `internal/services/dispatch/retry_policy.go`, `internal/services/dispatch/recovery_*.go`
- recovery policy model: `internal/recovery/policy.go`

### 5. Sandboxes and Agent Runtimes

Agents run inside either local worktrees or Daytona-backed sandboxes. Runtime support currently exists for Claude Code and Pi under the runtime layer.

Key code:

- sandbox abstraction: `internal/sandbox/provider.go`, `internal/sandbox/types.go`
- local sandbox: `internal/sandbox/local/`
- Daytona sandbox: `internal/sandbox/daytona/`
- runtime abstraction: `internal/runtime/runtime.go`
- Claude Code runtime: `internal/runtime/claudecode/`
- Pi runtime: `internal/runtime/pi/`

### 6. Merge, Observability, and Mail

Once streams are ready, the merge processor integrates them and re-runs checks. Operator-facing observability is recorded through the recorder and projected to events. Mail remains primarily for agent-to-human and human-to-agent coordination.

Key code:

- merge queue and processor: `internal/services/merge/`
- observability recorder: `internal/observability/recorder.go`
- event bus: `internal/services/events/`
- mail broker: `internal/services/mail/`
- watch/log routes: `internal/daemon/sse.go`, `cmd/tack/watch.go`, `cmd/tack/logs.go`

## Data Model Areas

Most durable state lives in `internal/db/`.

Important stores:

- projects: `projects.go`
- objectives: `objectives.go`
- plans and streams: `plans.go`, `streams.go`
- runs and executions: `runs.go`
- attempts / recovery ledger: `attempts.go`
- merge queue: `merge_queue.go`
- events: `events.go`
- mail: `mail.go`

## What Is Current vs Future

Current, code-backed:

- daemon-first multi-project architecture
- project-scoped config and registration
- blueprint-driven execution
- isolated sandboxes/worktrees
- planner/build/review flow
- quality gates
- retry and recovery infrastructure
- merge queue
- operator watch/log surfaces

Future direction, not yet first-class in code:

- explicit discovery stage before planning
- compact context dossiers
- bounded execution contracts derived from discovery
- stronger benchmark-driven context evaluation

When internal docs disagree with this file, trust the code paths above first and then update the docs.
