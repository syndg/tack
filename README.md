# Tack

**One objective in. Reviewed, tested, merged code out.**

Tack is an open-source harness for deterministic, repository-aware agentic code execution. You describe what you want built. Tack turns that into a bounded workflow: discovery, planning, isolated execution, quality gates, recovery, merge, and PR creation.

```
$ tack plan "Add pagination to all list endpoints and a PATCH /expenses/:id endpoint"

Created objective fb68742b. Planner agent will decompose.

$ tack approve e2b12fee

Plan approved. Execution will begin.
  Stream 1: PATCH expenses endpoint        ██████████ merged
  Stream 2: Pagination for list endpoints   ██████████ merged

Objective completed. PR created: github.com/you/project/pull/42
```

---

## Why Tack

The harness matters more than the model. Everything in Tack that isn't an LLM decision is deterministic infrastructure: blueprints, quality gates, scoped rules, file scope enforcement, merge ordering, recovery policy. The LLM is the worker. Tack is the harness around it.

Tack solves six problems:

- **Objectives are underspecified** — discovery produces a context dossier before planning
- **Planning is serial** — the planner decomposes into parallel streams with file scopes and dependencies
- **Execution is uncoordinated** — agents run in isolated worktrees with curated tools and bounded contracts
- **You must be present** — `tack daemon` runs agents in the background; you review results
- **Failures stall progress** — quality-gate and review failures trigger automatic recovery loops
- **Merging is manual** — the merge processor integrates branches with tiered conflict resolution

---

## Quickstart

```bash
# Install
git clone https://github.com/syndg/tack.git
cd tack && go build -o tack ./cmd/tack/

# Initialize a project
cd your-project && tack init

# Start the daemon (separate terminal)
tack daemon

# Submit an objective
tack plan "Add a health check endpoint at GET /health that returns 200 OK"

# Review and approve
tack show <plan-id>
tack approve <plan-id>

# Watch it work
tack watch
```

See the [full getting started guide](docs-site/content/docs/getting-started/index.mdx) for details.

---

## How It Works

```
Objective
    │
    ▼
 Discovery ──► Context Dossier (cited repo context, suggested seams)
    │
    ▼
  Planner ──► Plan (streams, file scopes, dependencies)
    │
    ▼
  Human Approval
    │
    ▼
  Dispatch ──► Parallel Agents in Isolated Worktrees
    │
    ├── Stream 1: Build ──► Quality Gates ──► Review ──► Merge Ready
    ├── Stream 2: Build ──► Quality Gates ──► Review ──► Merge Ready
    └── Stream 3: Build ──► Quality Gates ──► Review ──► Merge Ready
    │
    ▼
  Merge Queue ──► Post-Merge Gates ──► PR Created
    │
    ▼
 Objective Complete
```

Each step is either **deterministic** (quality gates, merge, scheduling, recovery) or **agentic** (discovery, planning, building, reviewing). Blueprints define which is which. You control the workflow.

### Agent Roles

| Role | What it does |
|------|-------------|
| **Discovery** | Explores the codebase, produces a cited context dossier for the objective |
| **Planner** | Consumes the dossier, decomposes the objective into parallel streams with file scopes |
| **Builder** | Implements a stream's task in an isolated worktree |
| **Reviewer** | Reviews the implementation for correctness and quality |
| **Merger** | Integrates stream branches, runs post-merge quality gates |

### Isolation Model

Each agent works in its own git worktree. File scope is enforced. Agents never see each other's changes during execution. Integration happens at merge time.

---

## Features

### Context Engineering

Discovery runs before planning. It produces a context dossier with cited repo context and suggested seams. The planner consumes that dossier to create better decompositions. When the dossier is insufficient, the planner can request expansion.

During execution, contract failures are typed and drive local repair. Repeated insights derive contract patches that are auto-applied within the objective. Codification candidates accumulate for later review.

### File-Attribution Quality Gates

Quality gates run against the full project, but Tack only fails a stream for errors in its own files. Pre-existing errors don't block progress. Cross-file breakage is caught at merge time.

### Recovery Loops

- Quality gate failures rerun the builder with retry context
- Reviewer rejection reruns the builder with feedback attached
- Transient runtime failures retry in place
- Exhausted retries block for human guidance instead of hot-looping
- Human-guided retries survive daemon restarts

### Blueprints

Workflow steps are YAML state machines. Steps can be `agent`, `deterministic`, `human`, or `blueprint_ref` (nested). Tack ships neutral defaults. Override them or add your own in `.tack/blueprints/`.

```yaml
id: build-review
name: Build and review
retry:
  profile: self_healing
steps:
  - id: build
    type: agent
    role: builder
    next: lint
  - id: lint
    type: deterministic
    action: run_quality_gates
    retry:
      max_attempts: 2
    on_fail: build
    next: review
  - id: review
    type: agent
    role: reviewer
    on_fail: build
    next: merge_ready
  - id: merge_ready
    type: deterministic
    action: signal_merge_ready
```

### Scoped Rules

Project conventions injected into agent prompts based on file scope. Every rule you add makes all future agents smarter.

```yaml
name: "Project conventions"
scope: all
rules:
  - id: use-bun
    description: "Always use Bun, never npm or pnpm"
  - id: integer-cents
    description: "Use integer cents for monetary amounts, never floating point"
    applies_to:
      - "src/payments/**"
```

### Observability

| Command | What it shows |
|---------|---------------|
| `tack watch` | Live stream of all agent activity |
| `tack watch --summary` | Status changes only |
| `tack watch --verbose` | Full tool args, messages, diffs |
| `tack logs <agent-id>` | Full replay from JSONL activity logs |

### Merge Queue

Tiered conflict resolution:

| Tier | Strategy | When |
|------|----------|------|
| 1 | Clean merge | No conflicts |
| 2 | Auto-resolve (favor incoming) | Textual conflicts in isolated file scopes |
| 3 | AI merge (planned) | Semantic conflicts |
| 4 | Human escalation | All else fails |

Dependencies between streams are respected.

### Tool Curation

Agents get a curated tool set based on role, file scope, and project config. A builder working on CSS doesn't need database tools.

---

## Proof

Tack is benchmarked against real open-source projects.

**lazygit: command-log navigation keybindings** — 3 streams, 3 merged, 0 recovery events, 18 minutes. Zero human intervention. First-pass success on every stream. ([full report](docs/benchmarks/2026-04-14-lazygit-command-log-nav-keybindings-2f5a4e99-8fc4-488e-baa7-ae69b17f0c4c.md))

**lazygit: undo basic commit/checkout** — 3 streams, 3 merged, 5 automatic recoveries (4 review rejections self-healed), final integration validation passed. Zero human intervention. ([full report](docs/benchmarks/2026-04-14-lazygit-undo-basic-commit-checkout-545e5673-1b4a-4527-b62a-da2542e08d6a.md))

---

## Security

Tack has been security audited. Key properties:

- Daemon binds to loopback only (`127.0.0.1:9800`) by default
- All daemon routes require bearer auth
- Agent callbacks are authenticated
- Sandbox path traversal is blocked (local and Daytona)
- Git credentials are not persisted in remote URLs
- Observability logs use `0600` permissions
- Mail endpoints enforce project scoping

See the [full security audit](docs/security-audit-2026-04-10.md) for details.

---

## CLI Reference

| Command | Description |
|---------|-------------|
| `tack daemon` | Start the machine-wide Tack daemon |
| `tack init` | Bootstrap Tack for a repo (interactive wizard) |
| `tack plan <description>` | Create an objective and generate a plan |
| `tack plans` | List all plans |
| `tack show <plan-id>` | Show plan details with streams |
| `tack approve <plan-id>` | Approve a plan for execution |
| `tack reject <plan-id>` | Reject a plan |
| `tack status` | Show daemon status |
| `tack agents` | List active agent sessions |
| `tack watch` | Live stream of agent activity |
| `tack logs <agent-id>` | Replay agent activity log |
| `tack mail` | View escalation and message history |
| `tack merge` | View merge queue status |
| `tack version` | Print version |

---

## Project Layout

```
.tack/
  config.yaml          # Runtime config: model, gates, concurrency, timeouts
  blueprints/          # Blueprint definitions (filenames are arbitrary)
    standard.yaml      #   Default top-level blueprint
    build-review.yaml  #   Reusable nested blueprint
  rules/               # Project-specific agent guidance, scoped by file patterns
    project.yaml
  data/                # Managed by Tack: SQLite, activity logs
    tack.db
    activity/          # Per-agent JSONL activity logs
```

---

## Runtime Support

| Runtime | Status | RPC | Hooks | File Scope |
|---------|--------|-----|-------|------------|
| [Pi](https://github.com/anthropics/pi) | Supported | Yes | Yes | Yes |
| Claude Code | Supported | No | Yes | Prompt-level |
| Codex CLI | Planned | Limited | No | Prompt-level |

## Sandbox Providers

| Provider | Status | Isolation | Use case |
|----------|--------|-----------|----------|
| Local (git worktrees) | Supported | Process-level | Development, testing |
| Daytona | Supported | VM-level | Production, remote |
| Docker | Planned | Container-level | Self-hosted |
| E2B | Planned | VM-level | Cloud |

---

## What Tack Is Not

- **Not an agent framework.** Pi and Claude Code do the thinking. Tack shapes the workflow around them.
- **Not a sandbox provider.** Local worktrees, Daytona, Docker provide isolation. Tack manages their lifecycle.
- **Not an IDE.** Agents edit code. You review their work.
- **Not a CI system.** Tack runs quality gates locally in sandboxes. CI is your existing pipeline.
- **Not locked to any model.** Bring your own runtime, provider, and model.

---

## Documentation

- [Getting Started](docs-site/content/docs/getting-started/index.mdx) — Install, init, first objective
- [Concepts](docs-site/content/docs/concepts/) — Objectives, streams, blueprints, isolation, merge queue, quality gates
- [Guides](docs-site/content/docs/guides/) — Credentials, recovery, observability, rules, custom blueprints, Daytona
- [Architecture](docs/CURRENT_ARCHITECTURE.md) — Internal code-oriented architecture map

---

## License

Apache-2.0 — see [LICENSE](LICENSE).
