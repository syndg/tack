# Deck

**One objective in. Reviewed, tested, merged code out.**

Deck is an open-source orchestrator that runs AI coding agents at scale. You describe what you want built. Deck decomposes it into parallel work streams, assigns each to an isolated agent, enforces quality gates, reviews the output, merges everything, and opens a PR.

You go from being the developer to being the team lead.

```
$ deck plan "Add pagination to all list endpoints and a PATCH /expenses/:id endpoint"

Created objective fb68742b. Planner agent will decompose.

$ deck approve e2b12fee

Plan approved. Execution will begin.
  Stream 1: PATCH expenses endpoint        ██████████ merged
  Stream 2: Pagination for list endpoints   ██████████ merged

Objective completed. PR created: github.com/you/project/pull/42
```

---

## Why Deck

**The harness matters more than the model.** LangChain improved from 52.8% to 66.5% on Terminal Bench 2.0 by modifying only the harness, not the model. Stripe and OpenAI built proprietary harnesses internally. Deck is that harness — open source and configurable.

Everything in Deck that isn't an LLM decision is deterministic infrastructure: the blueprint engine, quality gates, merge queue, scoped rules, file scope enforcement, timeout management. The LLM is the horse. Deck is the equipment.

**What Deck solves:**

- **Planning is serial** — Deck's planner decomposes objectives into parallel streams automatically
- **Execution is uncoordinated** — Deck manages agent lifecycle, scheduling, and isolation
- **You must be present** — Deck runs as a daemon. Agents work while you're away
- **Merging is manual** — Deck's merge processor integrates branches with tiered conflict resolution
- **Quality is inconsistent** — Deterministic gates enforce lint, typecheck, and tests on every change

---

## Getting Started

### Install

```bash
git clone https://github.com/syndg/deck.git
cd deck
go build -o deck ./cmd/deck/
```

### Configure your project

Create `.deck/config.yaml` in your project root:

```yaml
daemon:
  listen: "127.0.0.1:9800"
  data_dir: ".deck/data"
  base_branch: "main"

sandbox:
  provider: local
  # post_create:              # optional setup commands
  #   - "bun install"

agents:
  runtime: pi
  max_concurrent: 4
  stagger_delay_ms: 1000
  pi:
    provider: anthropic
    model: claude-opus-4-6
    thinking_level: medium
  timeouts:
    default:
      max_duration_minutes: 30
      idle_minutes: 10
    builder:
      max_duration_minutes: 45
    reviewer:
      max_duration_minutes: 15

quality_gates:
  - "bun test"
  - "bunx tsc --noEmit"
  - "bun run lint"
```

### Run

```bash
# Start the daemon
deck daemon --config .deck/config.yaml

# Submit an objective
deck plan "Refactor the auth module to use JWT"

# Review and approve
deck show <plan-id>
deck approve <plan-id>

# Watch agents work in real time
deck watch

# Check what a specific agent did
deck logs <agent-id>
```

---

## How It Works

### The Pipeline

```
Objective
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
    ├── Stream 1: Scout ──► Build ──► Quality Gates ──► Review ──► Merge Ready
    ├── Stream 2: Scout ──► Build ──► Quality Gates ──► Review ──► Merge Ready
    └── Stream 3: Scout ──► Build ──► Quality Gates ──► Review ──► Merge Ready
    │
    ▼
 Merge Queue ──► Post-Merge Gates ──► PR Created
    │
    ▼
 Objective Complete
```

Each step is either **deterministic** (quality gates, merge, scheduling) or **agentic** (planning, building, reviewing). The blueprint YAML defines which is which. You control the workflow.

### Agent Roles

| Role | What it does |
|------|-------------|
| **Planner** | Explores the codebase, decomposes the objective into parallel streams with file scopes and dependencies |
| **Scout** | Investigates specific files or patterns before building (optional, skippable) |
| **Builder** | Implements the stream's task in an isolated worktree, commits changes |
| **Reviewer** | Reviews the implementation for correctness and quality |
| **Merger** | Integrates stream branches, runs post-merge quality gates |

### Isolation Model

Each agent works in its own git worktree. File scope is enforced — agents can only modify files in their assigned scope. Worktrees are created from the latest main branch with gitignored files (node_modules, build caches) automatically copied from the main repo.

Agents never see each other's changes during execution. Integration happens at merge time through the merge processor.

---

## Features

### File-Attribution Quality Gates

Quality gates run against the full project, but Deck only fails a stream for errors in its own files. Pre-existing errors in other files don't block progress. Cross-file breakage is caught at merge time.

```
Stream 1 (backend routes):     bunx tsc --noEmit → fails on frontend error → PASS (not in scope)
Stream 2 (frontend components): bunx tsc --noEmit → fails on frontend error → FAIL (in scope, fix-loop)
```

### Blueprints

Workflows are defined as YAML state machines. Steps can be `agent`, `deterministic`, `human`, or `blueprint_ref` (nested).

Deck ships two defaults. Override by placing your own in `.deck/blueprints/`.

```yaml
# .deck/blueprints/stream.yaml — per-stream execution
steps:
  - id: scout
    type: agent
    role: scout
    optional: true
    next: build

  - id: build
    type: agent
    role: builder
    commit: auto
    next: lint

  - id: lint
    type: deterministic
    action: run_quality_gates
    retry: 2
    on_fail: build
    max_fix_iterations: 3
    next: review

  - id: review
    type: agent
    role: reviewer
    next: merge_ready

  - id: merge_ready
    type: deterministic
    action: signal_merge_ready
```

### Scoped Rules

Project conventions injected into agent prompts based on file scope. Every rule you add makes all future agents smarter.

```yaml
# .deck/rules/project.yaml
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

**`deck watch`** — Live stream of all agent activity with filters and verbosity levels.

```
18:16:12 [stream-1] builder: read src/schemas.ts
18:16:15 [stream-1] builder: edit src/routes/expenses.ts
18:16:19 [stream-2] builder: $ bunx tsc --noEmit
18:16:23 [stream-1] builder: done: Added PATCH endpoint with Zod validation
18:16:25 [stream-1] lint passed
18:16:30 [stream-1] reviewer spawned
18:47:35 [stream-1] merged ✓
```

**`deck logs <agent-id>`** — Full replay of what an agent did from JSONL activity logs. Supports `--follow` for live tailing.

**`deck watch --verbose`** — Shows full tool arguments, message content, and file diffs.

**`deck watch --summary`** — Objective-level status changes only.

### Escalation Dedup

When an agent hits the same blocker across fix-loop iterations, Deck sends one escalation — not 50 copies of the same error.

### Partial Completion

When some streams fail, Deck merges what succeeded and marks the objective `partial`. You can retry failed streams or accept the partial result. The pipeline doesn't stall waiting for streams that will never complete.

### Configurable Timeouts

Per-role max duration and idle timeout. An agent that stops producing output gets killed with a clear error message and triggers the fix-loop or escalation.

```yaml
agents:
  timeouts:
    default:
      max_duration_minutes: 30
      idle_minutes: 10
    builder:
      max_duration_minutes: 45
    reviewer:
      max_duration_minutes: 15
```

### Copy-Ignored

New worktrees automatically receive gitignored files from the main repo — node_modules, build caches, .env files. No cold starts. Uses reflink (copy-on-write) where available.

Control what gets copied with `.worktreeinclude`:

```text
# .worktreeinclude
node_modules/
.env
.next/
```

### Sandbox Cleanup

Worktrees and branches are automatically deleted when an objective reaches a terminal state. No more 1000+ stale worktrees eating disk.

### Merge Queue

Tiered conflict resolution:

| Tier | Strategy | When |
|------|----------|------|
| 1 | Clean merge | No conflicts |
| 2 | Auto-resolve (favor incoming) | Textual conflicts in isolated file scopes |
| 3 | AI merge (planned) | Semantic conflicts |
| 4 | Human escalation | All else fails |

Dependencies between streams are respected — stream 2 won't merge until stream 1 is merged if it depends on it.

### Tool Curation

Agents get a curated set of tools based on their role, file scope, and project config. A builder working on CSS doesn't need database tools.

```yaml
tools:
  max_per_agent: 15
  always_include: []
  always_exclude: []
```

---

## CLI Reference

| Command | Description |
|---------|-------------|
| `deck daemon` | Start the orchestrator daemon |
| `deck plan <description>` | Create an objective and generate a plan |
| `deck plan <desc> --simple` | Single-agent mode (no decomposition) |
| `deck plans` | List all plans |
| `deck show <plan-id>` | Show plan details with streams |
| `deck approve <plan-id>` | Approve a plan for execution |
| `deck reject <plan-id>` | Reject a plan |
| `deck exec <objective-id>` | Manually trigger execution |
| `deck status` | Show daemon status |
| `deck agents` | List active agent sessions |
| `deck watch` | Live stream of agent activity |
| `deck logs <agent-id>` | Replay agent activity log |
| `deck mail` | View escalation and message history |
| `deck merge` | View merge queue status |
| `deck version` | Print version |

---

## Project Layout

```
.deck/
  config.yaml          # Runtime config: model, gates, concurrency, timeouts
  blueprints/          # Workflow definitions (ships with sensible defaults)
    stream.yaml        #   Per-stream: scout → build → lint → review → merge_ready
  rules/               # Project-specific agent guidance, scoped by file patterns
    project.yaml
  data/                # Managed by Deck: SQLite, activity logs
    deck.db
    activity/          # Per-agent JSONL activity logs
```

---

## Pluggable Runtime

Deck supports multiple agent runtimes through a provider interface:

| Runtime | Status | RPC | Hooks | File Scope |
|---------|--------|-----|-------|------------|
| [Pi](https://github.com/anthropics/pi) | Supported | Yes (stdin/stdout JSON) | Yes (extension) | Yes (tool_call hook) |
| Claude Code | Supported | No | Yes (hooks system) | Prompt-level |
| Codex CLI | Planned | Limited | No | Prompt-level |

The runtime is configured per-project:

```yaml
agents:
  runtime: pi    # or "claude-code"
  pi:
    provider: anthropic
    model: claude-opus-4-6
    thinking_level: medium
```

---

## Sandbox Providers

| Provider | Status | Isolation | Use case |
|----------|--------|-----------|----------|
| Local (git worktrees) | Supported | Process-level | Development, testing |
| Daytona | Supported | VM-level | Production, remote |
| Docker | Planned | Container-level | Self-hosted |
| E2B | Planned | VM-level | Cloud |

---

## Design Documents

- [Orchestrator Design](docs/plans/2026-02-25-orchestrator-design.md) — Full architecture, principles, and data model
- [Gate Isolation](docs/plans/2026-03-14-gate-isolation-design.md) — File-attribution gates, escalation dedup, communication simplification
- [Observability](docs/plans/2026-03-14-observability-partial-completion-design.md) — Activity logging, watch/logs, timeouts, partial completion

---

## What Deck Is Not

- **Not an agent framework.** Deck doesn't implement agents. Pi and Claude Code do the thinking. Deck orchestrates them.
- **Not a sandbox provider.** Local worktrees, Daytona, Docker provide isolation. Deck manages their lifecycle.
- **Not an IDE.** Deck doesn't edit code. Agents edit code. You review their work.
- **Not a CI system.** Deck runs quality gates locally in sandboxes. CI is your existing pipeline.
- **Not locked to any model.** Bring your own runtime, provider, and model. The harness stays the same.

---

## License

MIT
