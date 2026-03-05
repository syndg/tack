# Deck — Agentic Workflow Orchestrator Design

**Date:** 2026-02-25
**Status:** Draft — Under Review
**Author:** SynDG + Claude
**Supersedes:** MVP Design (removed — TUI is now a client to the daemon, not the product)

---

## Evolution

The MVP defined Deck as a terminal-native TUI for embedding agent sessions, reviewing diffs, and running commands. This document evolves Deck into a **daemon-first agentic workflow orchestrator** — a system that manages the full lifecycle of AI-assisted coding work: **plan, execute, review, merge**.

The MVP's TUI becomes one client interface into a headless orchestration engine that runs anywhere.

### Deck as a Harness Engineering Framework

OpenAI's "harness engineering" discipline — building infrastructure, constraints, and feedback loops that enable AI agents to operate reliably — describes exactly what Deck is. But where OpenAI and Stripe built proprietary harnesses for themselves, **Deck is the open-source, configurable harness framework that lets anyone build their own.**

The four harness functions map directly to Deck's architecture:

| Harness Function | What It Means | Deck's Implementation |
|---|---|---|
| **Constrain** | Architectural boundaries, dependency rules | Blueprint state machine, file scope isolation, deterministic gates |
| **Inform** | Right context at the right time | Scoped rules, agent overlays, repo-aware context pipeline |
| **Verify** | Testing, linting, CI validation | Quality gates in sandboxes, max retry cap, independent verification |
| **Correct** | Feedback loops, self-repair | Watchdog triage, escalation chain, learned rules from failures |

**Key insight from the field:** LangChain improved from 52.8% to 66.5% on Terminal Bench 2.0 by modifying only the harness, not the model. The harness matters more than the model. Deck's value proposition is the harness — users bring their own models, sandboxes, and quality gates.

Everything in Deck that isn't an LLM decision is harness. The blueprint engine, the merge queue, the scoped rules, the tool curation, the watchdog — these are the deterministic infrastructure that makes agents reliable. The LLM is the horse; Deck is the equipment that channels its power.

---

## Problem

Solo developers using AI coding agents face compounding bottlenecks:

1. **Planning is serial.** You brainstorm with one agent, then manually translate the plan into tasks. You can't plan multiple things concurrently without losing context.
2. **Execution is uncoordinated.** Multiple agent sessions run in separate terminals with no awareness of each other. You are the router — context-switching between sessions, copy-pasting context, watching for completion.
3. **No team dynamics.** Large tasks (refactors, migrations, new features) require decomposition into parallel work streams. Today, you decompose manually and manage each stream yourself.
4. **You must be present.** Agents stop when they need input. If you're away, everything stalls. There's no way to let agents work overnight or while you're on your phone.
5. **Merging is manual.** Parallel agents produce parallel branches. Conflict resolution, test validation, and integration are entirely on you.

## Solution

Deck is a **daemon-first orchestrator** that turns a solo developer into a team lead. You define objectives, collaborate on plans, approve decompositions, and review results. Deck handles everything in between — spawning agent teams in isolated sandboxes, routing communication between them, managing merge conflicts, and keeping you informed from any device.

---

## Principles

Borrowed from the best of what we studied:

| Principle | Source | Application |
|-----------|--------|-------------|
| **Deterministic gates around agentic nodes** | Stripe blueprints | Lint, test, merge steps are code, not LLM decisions |
| **Hook-injected communication** | Overstory | Agents don't choose to check mail — it's forced via Pi hooks |
| **Hierarchical spawning with depth limits** | Overstory, OpenClaw | Coordinator → Leads → Workers. Max depth enforced. |
| **File scope isolation** | Overstory | Each agent owns specific files. Eliminates merge conflicts from overlap. |
| **Observable state as truth** | Overstory watchdog | Process alive? Sandbox running? Don't trust self-reported status. |
| **Shift feedback left** | Stripe | Lint/test in sandbox before merge queue, not after |
| **One-shot workers, persistent leads** | Stripe + Overstory | Depth-2 workers do one job and die. Leads persist across a task lifecycle. |
| **Daemon-first, TUI-as-client** | OpenClaw | Core runs headless on any machine. Multiple clients connect. |
| **Clean service architecture** | OpenCode | Pub/sub broker, SQLite persistence, typed events |
| **SDK-driven sandboxes** | Daytona | Programmatic sandbox lifecycle, not manual environment setup |
| **Configurable blueprints** | Stripe | Blueprints are user-defined YAML files, not hardcoded logic |
| **Scoped rules that compound** | Stripe, Hashimoto | Every mistake becomes a glob-scoped rule for future agents |
| **Tool curation per task** | Stripe Tool Shed | Daemon curates which tools each Worker gets, not all 500 |
| **Pluggable agent runtime** | Harness engineering | Runtime interface wraps Pi, Claude Code, Codex, or any agent |
| **Progressive autonomy** | Anthropic research | 4-tier autonomy system tied to the blueprint state machine |

---

## Transport Layer

All communication uses **HTTP REST + SSE**. No gRPC, no protobuf, no codegen.

| Path | Method | Purpose |
|------|--------|---------|
| Client → Daemon (commands) | HTTP REST | Create objectives, approve plans, send mail, steer agents |
| Daemon → Client (live events) | SSE (Server-Sent Events) | Dashboard updates, status changes, escalation notifications |
| Pi Extension → Daemon (from sandbox) | HTTP REST | Fetch unread mail, report status, signal completion. Uses `fetch()` — zero dependencies. |
| Daemon → Pi Agent (control) | Pi RPC (stdin/stdout JSON) | Send prompts, steer mid-execution. Runs over sandbox `Exec()`. |
| TUI ↔ Sandbox PTY (session view) | Sandbox Provider PTY API | Bidirectional terminal I/O. Daytona uses WebSocket internally; other providers may use `docker exec`, SSH, etc. Deck doesn't manage the transport — the provider does. |

**Why not gRPC:**
- One server, handful of clients — proto codegen overhead isn't justified
- Pi extension (TypeScript in sandbox) would need a gRPC client library; `fetch()` is zero-dep
- SSE handles the only server-push need (event stream to clients)
- PTY bidirectional streaming is handled by the sandbox provider, not by Deck's transport

---

## Architecture Overview

### Two Binaries, One System

```
deck daemon    — Headless orchestrator. Runs on VPS, home server, or laptop.
                 Manages agent lifecycle, planning, mail, merge queue, sandboxes.
                 Exposes HTTP REST API + SSE event stream.

deck           — TUI client (Bubble Tea). Connects to local or remote daemon.
                 Session viewer, diff review, plan editor, dashboard.
                 Also serves as CLI: deck plan, deck status, deck approve.
```

### System Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                         CLIENTS                                  │
│  ┌────────────┐  ┌────────────┐  ┌───────────────────────────┐  │
│  │  TUI       │  │  CLI       │  │  OpenClaw Agent           │  │
│  │  (laptop)  │  │  (scripts) │  │  (Telegram/WhatsApp/etc)  │  │
│  └─────┬──────┘  └─────┬──────┘  └────────────┬──────────────┘  │
└────────┼───────────────┼──────────────────────┼─────────────────┘
         │               │                      │
         └───────────────┼──────────────────────┘
                         │ HTTP + SSE
         ┌───────────────▼───────────────────────────────────────┐
         │                 DECK DAEMON                           │
         │                                                       │
         │  ┌─────────────────────────────────────────────────┐  │
         │  │              Service Layer                      │  │
         │  │                                                 │  │
         │  │  ┌──────────┐ ┌──────────┐ ┌────────────────┐  │  │
         │  │  │ Planner  │ │ Dispatch │ │   Lifecycle     │  │  │
         │  │  │ Service  │ │ Service  │ │   Manager       │  │  │
         │  │  └──────────┘ └──────────┘ └────────────────┘  │  │
         │  │  ┌──────────┐ ┌──────────┐ ┌────────────────┐  │  │
         │  │  │   Mail   │ │  Merge   │ │   Watchdog     │  │  │
         │  │  │  Broker  │ │  Queue   │ │   Monitor      │  │  │
         │  │  └──────────┘ └──────────┘ └────────────────┘  │  │
         │  │  ┌──────────┐ ┌──────────┐                     │  │
         │  │  │  Event   │ │  Agent   │                     │  │
         │  │  │  Bus     │ │  Registry│                     │  │
         │  │  └──────────┘ └──────────┘                     │  │
         │  └─────────────────────────────────────────────────┘  │
         │                                                       │
         │  ┌─────────────────────────────────────────────────┐  │
         │  │            Infrastructure Layer                 │  │
         │  │                                                 │  │
         │  │  ┌──────────────────┐ ┌───────────────────────┐│  │
         │  │  │ Sandbox Provider │ │  SQLite (WAL)         ││  │
         │  │  │ Interface        │ │  mail.db, sessions.db ││  │
         │  │  │                  │ │  plans.db, events.db  ││  │
         │  │  │ Implementations: │ │  merge_queue.db       ││  │
         │  │  │ ● Daytona (v1)  │ └───────────────────────┘│  │
         │  │  │ ○ Docker (future)│                          │  │
         │  │  │ ○ E2B (future)  │                          │  │
         │  │  │ ○ Local (future)│                          │  │
         │  │  └──────────────────┘                          │  │
         │  └─────────────────────────────────────────────────┘  │
         └───────────────────────────────────────────────────────┘
                         │
            ┌────────────┼────────────┐
            ▼            ▼            ▼
     ┌────────────┐┌────────────┐┌────────────┐
     │  Sandbox   ││  Sandbox   ││  Sandbox   │
     │  (any      ││  (any      ││  (any      │
     │  provider) ││  provider) ││  provider) │
     │ ┌────────┐ ││ ┌────────┐ ││ ┌────────┐ │
     │ │ Pi     │ ││ │ Pi     │ ││ │ Pi     │ │
     │ │ Agent  │ ││ │ Agent  │ ││ │ Agent  │ │
     │ │ (RPC)  │ ││ │ (RPC)  │ ││ │ (RPC)  │ │
     │ └────────┘ ││ └────────┘ ││ └────────┘ │
     └────────────┘└────────────┘└────────────┘
```

---

## Core Concepts

### 1. Objectives

An **objective** is the unit of work you give Deck. It's a natural language description of what you want done.

```
"Refactor the auth module to use JWT instead of session cookies"
"Fix all flaky tests in the payments package"
"Add rate limiting to all public API endpoints"
```

Objectives enter the system via TUI, CLI, or OpenClaw message. Each objective gets a unique ID and flows through the lifecycle: `planning → approved → executing → reviewing → completed`.

### 2. Plans

A **plan** is a structured decomposition of an objective into parallel work streams. Plans are produced by a planner agent (collaborative with you or autonomous) and must be approved before execution begins.

```yaml
# Stored in plans.db, rendered as YAML for human review
objective: "Refactor auth to JWT"
id: plan_abc123
status: pending_approval

streams:
  - id: stream_1
    title: "JWT token generation and validation"
    scope:
      files: ["src/auth/token.*", "src/auth/jwt.*"]
      description: "Create JWT signing/verification utilities"
    agents:
      scout: true        # explore first
      builders: 1
      reviewer: true
    dependencies: []

  - id: stream_2
    title: "Replace session middleware with JWT middleware"
    scope:
      files: ["src/middleware/auth.*", "src/middleware/session.*"]
      description: "Swap session-based auth for JWT bearer tokens"
    agents:
      scout: false        # spec provided by stream_1 findings
      builders: 1
      reviewer: true
    dependencies: [stream_1]   # waits for stream_1 to merge

  - id: stream_3
    title: "Update all API route handlers"
    scope:
      files: ["src/routes/**/*.ts"]
      description: "Update req.session references to req.user from JWT"
    agents:
      scout: false
      builders: 2          # parallel builders, split by subdirectory
      reviewer: true
    dependencies: [stream_2]

quality_gates:
  - "bun test"
  - "bun run lint"
  - "bun run typecheck"
```

**Plan lifecycle:**
- `draft` — Planner agent is working on it
- `pending_approval` — Waiting for you to review
- `approved` — You approved, ready for dispatch
- `executing` — Agents are working
- `completed` — All streams merged
- `failed` — Unrecoverable failure, needs human intervention

### 3. Agent Roles

Four agent capabilities, mapped to Pi agent instances:

| Role | Depth | Sandbox | Persistence | Purpose |
|------|-------|---------|-------------|---------|
| **Planner** | 0 | Optional (can run on daemon host) | Persistent per objective | Explores codebase, proposes plan, iterates with you |
| **Lead** | 1 | Daytona sandbox | Persistent per stream | Manages workers, writes specs, signals merge readiness |
| **Worker** | 2 | Daytona sandbox | Ephemeral (one-shot) | Scout, Builder, or Reviewer. Does one job, reports, dies. |
| **Merger** | 1 | Daytona sandbox | Ephemeral | Integrates branches, resolves conflicts, runs quality gates |

**Hierarchy enforcement:**
- Planner spawns only Leads
- Leads spawn only Workers
- Workers cannot spawn anything
- Max concurrent agents configurable (default: 8)

### 4. Sandboxes (Provider-Agnostic)

Each agent (except planner) runs in an isolated sandbox. Deck defines a **Sandbox Provider interface** — an abstraction over any sandbox backend. Daytona is the first implementation; future providers (Docker, E2B, Fly Machines, local git worktrees) implement the same interface.

#### Sandbox Provider Interface

```go
// SandboxProvider manages sandbox lifecycle. Implementations wrap specific
// backends (Daytona, Docker, E2B, local worktrees, etc).
type SandboxProvider interface {
    Create(ctx context.Context, opts CreateOpts) (Sandbox, error)
    Get(ctx context.Context, id string) (Sandbox, error)
    List(ctx context.Context, labels map[string]string) ([]Sandbox, error)
    Delete(ctx context.Context, id string) error
    CreateSnapshot(ctx context.Context, opts SnapshotOpts) (Snapshot, error)
}

// Sandbox is an isolated environment where an agent runs.
type Sandbox interface {
    ID() string
    Status() SandboxStatus

    // Process execution
    Exec(ctx context.Context, cmd string, opts ExecOpts) (ExecResult, error)

    // PTY for interactive sessions (TUI drop-in)
    CreatePTY(ctx context.Context, opts PTYOpts) (PTYHandle, error)
    ConnectPTY(ctx context.Context, sessionID string, handler PTYDataHandler) (PTYHandle, error)

    // Filesystem
    Upload(ctx context.Context, content []byte, path string) error
    Download(ctx context.Context, path string) ([]byte, error)
    ListFiles(ctx context.Context, path string) ([]FileInfo, error)

    // Git
    Clone(ctx context.Context, repo string, path string) error

    // Lifecycle
    Stop(ctx context.Context) error
    Start(ctx context.Context) error
}

// PTYHandle provides bidirectional terminal access to a sandbox.
type PTYHandle interface {
    SendInput(data string) error
    Resize(cols, rows int) error
    Wait() (ExecResult, error)
    Kill() error
    Disconnect() error
}

// CreateOpts configures a new sandbox.
type CreateOpts struct {
    Name       string
    Labels     map[string]string       // deck.objective, deck.stream, deck.role
    Snapshot   string                  // base snapshot to clone from
    Resources  ResourceSpec            // CPU, memory, disk
    EnvVars    map[string]string
    Volumes    []VolumeMount           // shared storage
    AutoStop   time.Duration           // auto-stop after idle
    AutoDelete time.Duration           // auto-delete after stopped
    Ephemeral  bool                    // delete on stop
}
```

The rest of Deck's codebase only ever touches these interfaces. The Daytona implementation lives in `internal/sandbox/daytona/`. Config selects the provider:

```yaml
sandbox:
  provider: "daytona"    # future: "docker", "e2b", "local"
  daytona:
    api_key: "${DAYTONA_API_KEY}"
    default_snapshot: "deck-myproject"
    # ...
```

#### Daytona Implementation

The Daytona provider wraps the Daytona TypeScript/Go SDK:

```go
// internal/sandbox/daytona/provider.go
sandbox, err := provider.Create(ctx, sandbox.CreateOpts{
    Name:     fmt.Sprintf("deck-%s-%s", objectiveID, agentRole),
    Snapshot: projectSnapshot,
    Labels:   map[string]string{
        "deck.objective": objectiveID,
        "deck.stream":    streamID,
        "deck.role":      "builder",
    },
    Resources: sandbox.ResourceSpec{CPU: 2, Memory: 4, Disk: 10},
    AutoStop:   60 * time.Minute,
    AutoDelete: 180 * time.Minute,
    Volumes: []sandbox.VolumeMount{{
        VolumeID:  sharedVolume.ID,
        MountPath: "/deck/shared",
        Subpath:   objectiveID,
    }},
})
```

Daytona's native PTY WebSocket API powers the TUI session view. When you drop into an agent session, Deck calls `sandbox.ConnectPTY()` which maps to Daytona's `createPty()` / `connectPty()` — bidirectional terminal streaming is handled entirely by Daytona's SDK internally. Deck never manages WebSocket connections directly.

#### Why Sandboxes Over Git Worktrees

- **Isolation:** Full OS-level isolation. Agent can't corrupt your repo or other agents.
- **Elastic:** Spin up 8 sandboxes in seconds. No local disk pressure.
- **Remote:** Sandboxes run on provider infrastructure. Daemon can be a $5 VPS.
- **Snapshots:** Pre-warm a snapshot with your repo + dependencies. New sandboxes start in <90ms with everything ready.
- **Volumes:** Shared persistent storage for cross-agent artifacts (specs, reports).
- **Cleanup:** Auto-stop, auto-archive, auto-delete. No orphaned environments.

**Snapshot strategy:**
```
Base snapshot: "deck-{project}"
  - Repo cloned
  - Dependencies installed
  - LSP servers configured
  - Pi agent runtime installed
  - Deck agent hooks pre-configured

Per-sandbox:
  - Clone from snapshot (<90ms via Daytona, varies by provider)
  - Checkout specific branch
  - Apply file scope restrictions
  - Inject agent overlay (role, task, mail config)
```

### 5. Mail System (Inter-Agent Communication)

SQLite-backed message broker on the daemon. Agents never talk directly — all mail routes through Deck.

**Schema:**
```sql
CREATE TABLE mail (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    from_agent  TEXT NOT NULL,
    to_agent    TEXT NOT NULL,       -- agent name or broadcast (@builders, @all)
    type        TEXT NOT NULL,       -- status, question, result, error, dispatch, worker_done, merge_ready, escalation
    payload     TEXT NOT NULL,       -- JSON
    objective   TEXT NOT NULL,       -- objective ID
    stream      TEXT,                -- stream ID (nullable for cross-stream)
    read        INTEGER DEFAULT 0,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_mail_to_unread ON mail(to_agent, read, created_at);
```

**Delivery mechanism:**

Agents run Pi with a custom Deck extension. The extension registers a `UserPromptSubmit` hook:

```
On every prompt submission:
  1. Extension calls daemon HTTP API: GET /mail/{agentName}/unread
  2. Daemon returns unread messages as JSON
  3. Extension formats as structured context block
  4. Injects into the prompt as prepended context
  5. Agent sees mail as part of its input — no choice to ignore it
```

The extension also registers Deck tools that appear in Pi's tool list:

```
deck.mail.send      — Send message to another agent or broadcast
deck.mail.reply     — Reply to a specific message
deck.status.report  — Report progress to lead/coordinator
deck.escalate       — Escalate issue to human (triggers notification)
deck.done           — Signal task completion with summary
```

**Why this works:** Agents don't need to "remember" to check mail. The hook fires on every interaction, injecting context automatically. The tools are available but the critical path (receiving mail) doesn't depend on agents using them.

**Broadcast groups:**
- `@all` — Every agent in the objective
- `@stream:{id}` — All agents in a stream
- `@builders` — All builder agents
- `@leads` — All lead agents
- `@human` — Escalation to you (triggers TUI/OpenClaw notification)

### 6. Blueprints (Configurable Deterministic + Agentic Workflows)

Inspired by Stripe's blueprint pattern: a state machine that interleaves deterministic code steps with agentic (LLM-driven) steps.

**Blueprints are user-configurable YAML files, not hardcoded logic.** They live in `.deck/blueprints/` per project or `~/.config/deck/blueprints/` globally. Deck ships sensible defaults; users write their own for their specific workflows.

```yaml
# .deck/blueprints/feature.yaml — shipped default
name: "Feature Implementation"
description: "Plan, build, review, and merge a new feature"
trigger: "default"  # used when no blueprint is specified

steps:
  - id: plan
    type: agent        # LLM-driven
    role: planner
    description: "Explore codebase and decompose into streams"
    next: approve

  - id: approve
    type: human        # requires human approval
    description: "Review and approve the plan"
    next: dispatch

  - id: dispatch
    type: deterministic  # code, not LLM
    action: dispatch_streams
    description: "Spawn sandboxes and agents per stream"
    next: per_stream

  - id: per_stream
    type: blueprint_ref
    ref: ".deck/blueprints/stream.yaml"  # nested blueprint per stream
    next: merge

  - id: merge
    type: deterministic
    action: merge_queue
    description: "Merge all stream branches, run quality gates"
    next: complete

  - id: complete
    type: deterministic
    action: mark_complete
```

```yaml
# .deck/blueprints/stream.yaml — per-stream execution
name: "Stream Execution"
steps:
  - id: scout
    type: agent
    role: scout
    optional: true     # skip if plan says scout: false
    next: build

  - id: build
    type: agent
    role: builder
    next: lint

  - id: lint
    type: deterministic
    action: run_quality_gates
    retry: 2           # max retries before escalation
    next: review

  - id: review
    type: agent
    role: reviewer
    next: merge_ready

  - id: merge_ready
    type: deterministic
    action: signal_merge_ready
```

```yaml
# .deck/blueprints/hotfix.yaml — user-defined for quick fixes
name: "Hotfix"
description: "Single-agent fix with minimal ceremony"
trigger: "manual"

steps:
  - id: fix
    type: agent
    role: builder
    description: "Fix the issue in a single agent session"
    next: lint

  - id: lint
    type: deterministic
    action: run_quality_gates
    retry: 2
    next: merge

  - id: merge
    type: deterministic
    action: merge_queue
    next: complete

  - id: complete
    type: deterministic
    action: mark_complete
```

**Blueprint selection:** The Planner can recommend a blueprint based on task complexity (simple bug = `hotfix.yaml`, multi-stream feature = `feature.yaml`). Users can also specify: `deck plan "fix the auth bug" --blueprint hotfix`.

**Step types:**
- `agent` — LLM-driven. Spawns an agent with the specified role.
- `deterministic` — Code. Runs a predefined action (quality gates, merge, dispatch).
- `human` — Blocks until human approval via TUI/CLI/OpenClaw.
- `blueprint_ref` — Nests another blueprint (for per-stream execution within an objective).

**Why configurable:** Every team's workflow is different. Some want mandatory code review agents. Others trust CI and skip review. Some need security scanning steps. Others need database migration validation. The blueprint engine is the harness — users configure it for their project.

```
┌─────────────────────────────────────────────────────────┐
│                   OBJECTIVE BLUEPRINT                    │
│                                                         │
│  ┌─────────┐    ┌──────────┐    ┌────────────────────┐ │
│  │ PLAN    │───▶│ APPROVE  │───▶│ DISPATCH STREAMS   │ │
│  │ (agent) │    │ (human)  │    │ (deterministic)    │ │
│  └─────────┘    └──────────┘    └─────────┬──────────┘ │
│                                           │             │
│                              ┌────────────▼──────────┐ │
│                              │   PER-STREAM BLUEPRINT │ │
│                              │                        │ │
│   ┌─────────┐  ┌──────────┐ │  ┌───────┐ ┌────────┐ │ │
│   │ SCOUT   │─▶│ SPEC     │─┼─▶│ BUILD │▶│  LINT  │ │ │
│   │ (agent) │  │ (agent)  │ │  │(agent)│ │ (code) │ │ │
│   └─────────┘  └──────────┘ │  └───────┘ └───┬────┘ │ │
│                              │                │      │ │
│   ┌────────────────┐         │  ┌─────────┐   │      │ │
│   │ MERGE READY    │◀────────┼──│ REVIEW  │◀──┘      │ │
│   │ (deterministic)│         │  │ (agent) │          │ │
│   └───────┬────────┘         │  └─────────┘          │ │
│           │                  └───────────────────────┘ │
│           ▼                                            │
│  ┌────────────────┐    ┌──────────┐    ┌───────────┐  │
│  │ MERGE          │───▶│ CI/TEST  │───▶│ COMPLETE  │  │
│  │ (deterministic)│    │ (code)   │    │           │  │
│  └────────────────┘    └──────────┘    └───────────┘  │
└─────────────────────────────────────────────────────────┘

Legend:  (agent) = LLM-driven    (code) = deterministic    (human) = requires approval
```

**What's deterministic (never an LLM decision):**
- Sandbox provisioning and teardown
- Git branch creation and checkout
- Linter/formatter execution
- Test suite execution
- Merge operations (tier 1-2)
- Quality gate enforcement
- Mail routing
- Agent lifecycle (start, stop, timeout)

**What's agentic (LLM-driven):**
- Codebase exploration (scout)
- Plan decomposition (planner)
- Spec writing (lead)
- Implementation (builder)
- Code review (reviewer)
- Conflict resolution tier 3 (AI merge)
- Escalation triage

### 7. Merge Queue

FIFO merge queue with tiered conflict resolution:

| Tier | Strategy | Automated? | When |
|------|----------|------------|------|
| 1 | Clean merge (`git merge --no-edit`) | Yes | No conflicts |
| 2 | Auto-resolve (keep incoming for non-overlapping sections) | Yes | Textual conflicts in separate sections |
| 3 | AI-resolve (Pi agent analyzes context, resolves semantically) | Yes | Semantic conflicts |
| 4 | Human review | No | Tier 3 fails, escalate to you |

**Flow:**
1. Lead signals `merge_ready` for a stream's branch
2. Deck enqueues in merge queue
3. Processes FIFO — sequential for deterministic history
4. Runs quality gates after merge (tests, lint, typecheck)
5. On failure: retry once with a fixer agent, then escalate
6. On success: mark stream as merged, notify lead

**Dependency handling:** If stream_2 depends on stream_1, stream_2's merge is held until stream_1 merges. Deck enforces this from the plan's dependency graph.

### 8. Scoped Rules (`.deck/rules/`)

**Every mistake becomes a rule.** When a Worker fails and a human corrects it, the pattern should be captured as a scoped rule that prevents future agents from making the same mistake. This is the compounding loop that makes the harness smarter over time.

Rules live in `.deck/rules/` as markdown files with glob-scoped frontmatter:

```yaml
# .deck/rules/auth-patterns.md
---
scope: "src/auth/**"
---

- Use the existing `AuthProvider` class — do NOT create a new auth abstraction.
- JWT tokens must be signed with RS256, not HS256.
- All auth endpoints require rate limiting via the `rateLimiter` middleware.
- Token expiry is 15 minutes for access tokens, 7 days for refresh tokens.
```

```yaml
# .deck/rules/testing.md
---
scope: "**/*.test.ts"
---

- Use vitest, not jest. Import from 'vitest'.
- Use `vi.fn()` for mocks, not `jest.fn()`.
- Integration tests go in `__tests__/integration/`, unit tests colocate with source.
```

```yaml
# .deck/rules/payments.md
---
scope: "src/payments/**"
priority: high
---

- NEVER modify payment amount calculations without explicit human approval.
- All payment mutations must be idempotent (use idempotency keys).
- Log all payment state transitions to the audit table.
```

**How rules are delivered to agents:**

When the daemon constructs an agent's context package, it scans `.deck/rules/` for files whose `scope` glob matches the agent's assigned files. Matching rules are injected into the agent overlay alongside the task spec. The agent sees them as part of its system instructions — not optional reading.

```
Agent overlay for builder-payments-1:
  1. Role definition (builder)
  2. Task spec (from lead)
  3. File scope (src/payments/**)
  4. Matched rules:           ← auto-attached
     - .deck/rules/payments.md (scope: src/payments/**)
     - .deck/rules/testing.md  (scope: **/*.test.ts)
  5. Quality gates
  6. Communication config
```

**Rule sources:**
- **Manual:** Developer writes rules based on project conventions
- **Learned (future):** When a human corrects an agent and the correction reveals a pattern, the daemon prompts: "Save this as a rule for `src/auth/**`?" Stored in `.deck/rules/learned/` with timestamp and provenance.
- **Inherited:** Global rules in `~/.config/deck/rules/` apply to all projects (e.g., "always use Bun, not npm")

**Priority levels:** Rules can have `priority: high` which makes them appear at the top of the agent's context and are highlighted as constraints the agent must not violate.

**Why this matters:** Stripe uses conditional `.mdc` rule files with glob patterns that auto-attach as agents traverse the filesystem. Mitchell Hashimoto's core insight: "anytime you find an agent makes a mistake, you engineer a solution such that the agent never makes that mistake again." Scoped rules are that engineering, version-controlled and shared across the team.

### 9. Tool Curation

Stripe built "Tool Shed" — a meta-MCP server managing ~500 internal tools — because loading all tools into every agent kills performance. As Deck becomes provider-agnostic and users bring their own MCP servers, the daemon needs the same tool selection layer.

**The problem:** A project might have MCP servers for GitHub, database, Sentry, Figma, Slack, and custom internal tools. A Worker fixing a CSS bug doesn't need database tools. A Worker writing migrations doesn't need Figma tools. Loading everything wastes tokens and confuses the agent.

**Solution: tool scoping in blueprints and rules.**

Per-blueprint-step tool restrictions:
```yaml
# In a blueprint step
- id: build
  type: agent
  role: builder
  tools:
    include: ["mcp:github:*", "mcp:filesystem:*"]
    exclude: ["mcp:slack:*", "mcp:figma:*"]
```

Per-rule tool restrictions:
```yaml
# .deck/rules/database.md
---
scope: "src/db/**"
tools:
  include: ["mcp:database:*", "mcp:prisma:*"]
---
```

Global tool budget in config:
```yaml
# .deck/config.yaml
tools:
  max_per_agent: 15          # max tools exposed to any single agent
  always_include:             # always available to all agents
    - "mcp:filesystem:*"
    - "mcp:github:create_pr"
  always_exclude:             # never available to agents
    - "mcp:slack:send_message"
```

**How it works:** When the daemon constructs a Worker's environment, it resolves the effective tool set: blueprint step tools + matched rule tools + global config, deduplicated and capped at `max_per_agent`. The resulting tool list is passed to the agent runtime. Tools outside this list are not registered in the agent's session.

**Why 15 tools max:** Stripe found that curating ~15 relevant tools per task (from 500+ available) was the sweet spot. Beyond that, agents spend tokens reasoning about tool selection instead of the actual task.

### 10. Watchdog & Health Monitoring

Three-tier health system:

**Tier 0 — Mechanical (daemon goroutine, every 30s):**
- Is the sandbox still running? (via `SandboxProvider.Get()`)
- Is the Pi process alive inside the sandbox?
- Has the agent produced output in the last N minutes?
- Actions: log warning → nudge agent → restart sandbox → escalate to human

**Tier 1 — AI Triage (on-demand):**
- Spawned when Tier 0 detects repeated failures
- Ephemeral Pi agent reads logs, mail history, last output
- Classifies: stuck? crashed? waiting for input? scope too large?
- Recommends: retry, reassign, reduce scope, escalate

**Tier 2 — You:**
- Notification via TUI or OpenClaw
- You can: steer the agent, restart it, reassign the task, or take over manually

### 11. Event Bus

All services communicate through a typed pub/sub event bus:

```go
type Event struct {
    Type      EventType   // plan.created, agent.spawned, mail.sent, merge.completed, etc.
    Objective string
    Stream    string      // optional
    Agent     string      // optional
    Payload   any         // typed per event
    Timestamp time.Time
}
```

**Subscribers:**
- TUI client (via SSE stream) — live dashboard updates
- Watchdog — monitors agent health events
- Merge queue — listens for merge_ready signals
- Dispatcher — listens for plan approvals to spawn agents
- OpenClaw bridge — forwards escalations and status updates

**Persistence:** All events stored in `events.db` for replay, debugging, and audit trail.

---

## Planning System (Deep Dive)

The planning system is the bridge between your intent and agent execution.

### Planning Modes

**Focused (interactive):**
```
You: deck plan "Refactor auth to JWT"
Deck: Spawns planner agent, opens interactive session
      Planner explores codebase, asks you questions
      You iterate back and forth
      Planner produces structured plan
      You review in TUI, edit if needed, approve
      Deck dispatches agent teams
```

**Batch (queued):**
```
You: deck plan "Fix flaky tests in payments" --auto
     deck plan "Add rate limiting to public API" --auto
     deck plan "Update deps to latest major versions" --auto
Deck: Queues 3 planning sessions
      Each planner explores independently
      Plans appear in your approval queue
      You review/approve each as they're ready
      Approved plans dispatch immediately
```

**The `--auto` flag** means: "Don't wait for me during planning. Do your best, I'll review the plan before execution starts." This is the key to batch mode — planners work autonomously, but execution still requires your approval.

### Plan Editing

Plans are structured YAML (rendered from the plan object). You can edit them before approval:

- Add/remove streams
- Adjust file scopes
- Change agent counts
- Modify dependencies
- Add/remove quality gates
- Annotate with guidance ("use the existing AuthProvider, don't create a new one")

The TUI provides a structured editor for plans (not raw YAML editing). CLI users can export to YAML, edit, and re-import.

---

## OpenClaw Integration

Deck registers as a standard OpenClaw agent. OpenClaw handles all messaging platform concerns.

### Binding

```yaml
# In OpenClaw's config
agents:
  list:
    - id: deck
      name: "Deck"
      workspace: "~/.deck/openclaw-workspace"
      identity:
        emoji: "🎛️"

  bindings:
    - agentId: deck
      match:
        channel: telegram
        peer:
          kind: direct
          id: "your_telegram_id"
```

### How It Works

1. You message Deck via Telegram: "Plan a refactor of the auth module to JWT"
2. OpenClaw routes to the Deck agent
3. Deck agent translates the message into a daemon HTTP call: `POST /objectives` with body `"Refactor auth to JWT"`
4. Daemon spawns planner, works autonomously (batch mode)
5. When plan is ready: Deck agent messages you back via Telegram with the plan summary
6. You reply: "Looks good, approve" or "Change stream 2 to not depend on stream 1"
7. Deck agent calls `POST /plans/{id}/approve` or `PATCH /plans/{id}`
8. Execution begins. Status updates flow back through OpenClaw.
9. On escalation: Deck agent sends you the issue via Telegram, you respond inline.

### What Deck Exposes to OpenClaw

A thin adapter agent that translates natural language into daemon HTTP calls:

```
"plan X"           → POST   /objectives          {description: X}
"status"           → GET    /status
"approve plan 3"   → POST   /plans/3/approve
"show plan 3"      → GET    /plans/3
"stop objective 2" → POST   /objectives/2/stop
"steer builder-1: use existing middleware" → POST /mail {to: "builder-1", ...}
```

This agent is lightweight — it's a router, not an orchestrator. The daemon does all the real work.

---

## Data Model

All state in SQLite with WAL mode for concurrent access.

### Databases

| Database | Purpose | Key Tables |
|----------|---------|------------|
| `objectives.db` | Objective and plan lifecycle | objectives, plans, streams |
| `agents.db` | Agent registry and sessions | agents, sessions, sandboxes |
| `mail.db` | Inter-agent communication | mail |
| `events.db` | Event log and audit trail | events |
| `merge_queue.db` | Merge queue state | queue_entries |

### Key Relationships

```
Objective (1) ──── (1) Plan
Plan (1) ──── (N) Stream
Stream (1) ──── (N) Agent Session
Agent Session (1) ──── (1) Sandbox (provider-agnostic)
Agent Session (N) ──── (N) Mail Messages
```

---

## Agent Runtime (Provider-Agnostic)

Deck's harness is runtime-agnostic. Pi is the first supported runtime, but the architecture supports any agent that can run in a sandbox, receive prompts, and report results.

### Agent Runtime Interface

```go
// AgentRuntime abstracts over different agent runtimes.
// Implementations wrap specific agent tools (Pi, Claude Code, Codex CLI, etc).
type AgentRuntime interface {
    // Spawn starts an agent process in the given sandbox.
    Spawn(ctx context.Context, sandbox Sandbox, opts AgentOpts) (AgentProcess, error)
    // Name returns the runtime identifier ("pi", "claude-code", "codex", etc).
    Name() string
    // SupportsRPC returns whether the runtime supports mid-execution steering.
    SupportsRPC() bool
    // SupportsHooks returns whether the runtime supports hook injection (mail, scope).
    SupportsHooks() bool
}

type AgentProcess interface {
    // Send sends a prompt or steering message to the agent.
    Send(ctx context.Context, msg AgentMessage) error
    // Output returns a channel of agent output events.
    Output() <-chan AgentEvent
    // Wait blocks until the agent completes.
    Wait() (AgentResult, error)
    // Kill terminates the agent process.
    Kill() error
}

type AgentOpts struct {
    Role        string            // planner, lead, builder, reviewer, etc.
    Overlay     string            // system prompt overlay (markdown)
    Tools       []string          // curated tool list for this agent
    Rules       []string          // matched scoped rules (markdown content)
    Model       string            // model override (from config)
    EnvVars     map[string]string // runtime-specific env vars
    WorkDir     string            // working directory inside sandbox
}
```

**Runtime selection in config:**
```yaml
agents:
  runtime: "pi"                # default runtime
  # future: "claude-code", "codex", "aider", "custom"
  runtimes:
    pi:
      # Pi-specific config
    claude-code:
      # Claude Code-specific config (future)
      api_key: "${ANTHROPIC_API_KEY}"
```

**Why this matters for open source:** Users shouldn't be locked into one agent runtime. A team using Claude Code locally should be able to use Deck's orchestration without switching to Pi. A team with Codex access should be able to plug that in. The harness (blueprints, rules, gates, merge queue) stays the same — only the runtime that executes agent steps changes.

**Capability differences across runtimes:**

| Capability | Pi (RPC) | Claude Code (future) | Codex CLI (future) |
|---|---|---|---|
| Mid-execution steering | Yes (stdin/stdout JSON) | Yes (--rpc flag) | Limited |
| Hook injection (mail) | Yes (extension hooks) | Yes (hooks system) | No (prompt-level only) |
| File scope enforcement | Yes (tool_call hook) | Yes (permission rules) | No (prompt-level only) |
| Tool registration | Yes (extension tools) | Yes (MCP) | Limited |

Runtimes that don't support hooks get mail and rules injected via prompt prepending instead — less reliable but functional. The daemon tracks which delivery mechanism each runtime supports and adapts.

### Pi Runtime (First Implementation)

Each agent is a Pi instance running in RPC mode inside a sandbox.

### Deck Extension for Pi

A Pi extension (`deck-agent-extension`) is pre-installed in the Daytona snapshot. It:

1. **Registers hooks:**
   - `UserPromptSubmit`: Injects unread mail from daemon
   - `before_agent_start`: Loads agent overlay (role, scope, task spec)
   - `tool_call`: Enforces file scope restrictions
   - `agent_end`: Reports completion status to daemon

2. **Registers tools:**
   - `deck.mail.send(to, type, payload)` — Send mail
   - `deck.mail.reply(messageID, payload)` — Reply to message
   - `deck.status(summary)` — Report progress
   - `deck.escalate(severity, context)` — Escalate to human
   - `deck.done(summary, filesModified)` — Signal completion

3. **Communicates with daemon:**
   - Via HTTP to the daemon's address (passed as `DECK_DAEMON_URL` env var)
   - Auth via per-agent token (passed as `DECK_AGENT_TOKEN` env var, generated at spawn time)
   - Uses `fetch()` — no special client libraries needed in the sandbox

### Agent Overlay

Each agent gets a system prompt overlay injected via the extension's `before_agent_start` hook:

```markdown
# Deck Agent: builder-auth-1

## Role
You are a Builder agent. Your job is to implement code changes according to the spec.

## Task
Objective: Refactor auth to JWT
Stream: JWT token generation and validation
Spec: /deck/shared/specs/stream_1_spec.md

## File Scope
You may ONLY modify these files:
- src/auth/token.ts
- src/auth/jwt.ts
- src/auth/token.test.ts
- src/auth/jwt.test.ts

## Quality Gates
Before signaling completion, you MUST pass:
- bun test src/auth/
- bun run lint src/auth/
- bun run typecheck

## Communication
- Your lead is: lead-auth
- Use deck.status() to report progress
- Use deck.escalate() if you're blocked
- Use deck.done() when finished
- Check mail for updates from your lead (injected automatically)

## Constraints
- Do NOT modify files outside your scope
- Do NOT push to git (Deck handles merging)
- Do NOT install new dependencies without escalating
- Commit frequently with descriptive messages
```

### RPC Control

Deck daemon communicates with Pi agents via Pi's RPC mode (stdin/stdout JSON protocol):

```go
// Deck daemon sends initial prompt to Pi agent
agent.Send(RPCMessage{
    Type: "prompt",
    Content: "Begin implementation. Read the spec at /deck/shared/specs/stream_1_spec.md",
})

// Deck daemon can steer mid-execution
agent.Send(RPCMessage{
    Type: "steer",
    Content: "Use the existing bcrypt utility in src/utils/crypto.ts instead of adding a new dependency",
})
```

---

## Autonomy Levels

Deck implements progressive autonomy tied to the blueprint state machine. Higher autonomy = fewer human gates. Default is conservative; users unlock more autonomy as trust builds.

| Level | Name | Plan | Execution | Review | Merge | Best For |
|-------|------|------|-----------|--------|-------|----------|
| 0 | **Supervised** | Human approves | Human reviews each Worker output | Human reviews | Human approves | New projects, unfamiliar codebases |
| 1 | **Guided** | Human approves | Workers autonomous with deterministic gates | Human reviews final PR | Human approves | Default after test coverage established |
| 2 | **Monitored** | Auto-generated, human reviews | Autonomous with retry cap | Auto-review if diff < threshold | Auto-merge if CI green | Projects with strong test suites |
| 3 | **Autonomous** | Fully automatic | Fully automatic | Automatic | Auto-merge | Pre-defined task types only (deps, docs, tests) |

**Configuration:**
```yaml
autonomy:
  default_level: 1              # project-wide default
  overrides:
    - pattern: "deps:*"         # dependency updates
      level: 3
    - pattern: "docs:*"         # documentation
      level: 3
    - scope: "src/payments/**"  # security-sensitive paths
      level: 0                  # always supervised
```

**Task classification:** The Planner emits a complexity score and risk assessment with each plan. Classification criteria: number of files modified, presence of security-sensitive patterns (auth, payments, crypto), test coverage of affected code, whether the change is additive vs. modifying existing behavior. The daemon routes to the appropriate autonomy level based on classification + config.

**Stuck detection (enforced at all autonomy levels):**
1. **Repeater** — same tool call with same arguments twice in a row → inject "try a different approach"
2. **Spinner** — no file modifications after N tool calls → force progress report to Lead
3. **Timeout** — hard wall clock limit per Worker (configurable, default 30 min)

After 3 consecutive stuck signals: escalate to Lead. After Lead failure: escalate to human via SSE notification regardless of autonomy level. **Autonomy never means unmonitored.**

## Simple Mode (Single-Agent Escape Hatch)

Not every task needs the full Planner → Lead → Worker hierarchy. Simple mode collapses the pipeline to a single agent in a single sandbox — no decomposition, no streams, no inter-agent communication.

```
deck plan "fix the typo in README" --simple
```

Simple mode is:
- One objective → one agent → one sandbox → one branch
- Blueprint: `hotfix.yaml` (or any single-step blueprint)
- Quality gates still run (deterministic, non-negotiable)
- Merge queue still processes the result
- No Lead, no Workers, no mail system

**Why this matters:** The research consistently shows multi-agent benefits diminish as model capabilities improve. A single frontier model handles 80% of tasks better than a coordinated team of lesser agents. Simple mode proves value immediately without requiring users to understand the full hierarchy. Power users unlock the full orchestration for genuinely complex, multi-stream work.

**Auto-detection (future):** The Planner could assess task complexity and automatically recommend simple mode for small tasks. "This looks like a single-file fix. Run in simple mode?" The user can override.

---

## TUI Client

The TUI connects to the daemon via HTTP (commands) and SSE (live event stream) and provides:

### Dashboard View (default)

```
┌─────────────────────────────────────────────────────────────────┐
│ DECK                                          ▲ 3 objectives   │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ● Refactor auth to JWT                          [EXECUTING]    │
│    ├─ stream-1: JWT utilities          ██████████░░ 80%         │
│    │  ├─ scout-1                       ✓ completed              │
│    │  ├─ builder-1                     ◉ working (12m)          │
│    │  └─ reviewer-1                    ○ waiting                │
│    ├─ stream-2: JWT middleware         ░░░░░░░░░░░░ blocked     │
│    │  └─ (waiting on stream-1)                                  │
│    └─ stream-3: Update routes          ░░░░░░░░░░░░ blocked     │
│                                                                 │
│  ● Fix flaky tests                               [PLANNING]    │
│    └─ planner                          ◉ exploring              │
│                                                                 │
│  ● Add rate limiting                       [PENDING APPROVAL]   │
│    └─ plan ready — press Enter to review                        │
│                                                                 │
├─────────────────────────────────────────────────────────────────┤
│ [NORMAL] 5 agents running | 2 sandboxes idle | $0.47 today     │
└─────────────────────────────────────────────────────────────────┘
```

### Session View

Drop into any agent's Pi session via sandbox PTY:

- Select agent from dashboard → Enter → insert mode
- TUI calls `sandbox.ConnectPTY()` — the sandbox provider handles the transport (Daytona uses WebSocket internally, Docker would use `docker exec`, etc.)
- Keystrokes forwarded via `ptyHandle.SendInput()`, output rendered via `onData` callback
- Terminal resize propagated via `ptyHandle.Resize()`
- `Esc` to return to dashboard
- Can steer the agent in real-time

### Plan Review View

Structured plan editor:
- View proposed streams, scopes, dependencies
- Edit inline (add guidance, adjust scopes)
- Approve / reject / request re-plan

### Diff View

Same as MVP design — hunk-level review of changes across all streams:
- Filter by stream, agent, or file
- Stage/unstage/discard hunks
- View merge queue status

### Keybindings (Normal Mode)

| Key | Action |
|-----|--------|
| `j/k` | Navigate objectives/agents |
| `Enter` | Drop into agent session / open plan review |
| `d` | Diff view (filtered to selected context) |
| `p` | Plan review for selected objective |
| `m` | Merge queue view |
| `l` | Live event log |
| `?` | Help |
| `q` | Back / dismiss |
| `Esc` | Normal mode |
| `i` | Insert mode (in session view) |

---

## Project Structure

```
deck/
├── cmd/
│   ├── daemon/
│   │   └── main.go              # deck daemon entry point
│   └── deck/
│       └── main.go              # TUI + CLI entry point
├── internal/
│   ├── daemon/
│   │   ├── daemon.go            # Daemon lifecycle, HTTP server
│   │   ├── routes.go            # HTTP route definitions
│   │   ├── sse.go               # SSE event stream handler
│   │   └── config.go            # Daemon configuration
│   ├── sandbox/
│   │   ├── provider.go          # SandboxProvider + Sandbox interfaces
│   │   ├── types.go             # CreateOpts, PTYHandle, etc.
│   │   ├── daytona/
│   │   │   └── provider.go      # Daytona SDK implementation
│   │   └── docker/
│   │       └── provider.go      # (future) Docker implementation
│   ├── runtime/
│   │   ├── runtime.go           # AgentRuntime + AgentProcess interfaces
│   │   ├── pi/
│   │   │   ├── runtime.go       # Pi runtime implementation
│   │   │   ├── rpc.go           # Pi RPC client (stdin/stdout JSON)
│   │   │   └── hooks.go         # Hook definitions (mail inject, scope enforce)
│   │   └── claudecode/
│   │       └── runtime.go       # (future) Claude Code runtime implementation
│   ├── harness/
│   │   ├── blueprint/
│   │   │   ├── engine.go        # Blueprint YAML loader + state machine executor
│   │   │   ├── types.go         # Step types (agent, deterministic, human, ref)
│   │   │   └── defaults/        # Shipped default blueprints
│   │   │       ├── feature.yaml
│   │   │       ├── hotfix.yaml
│   │   │       └── stream.yaml
│   │   ├── rules/
│   │   │   ├── engine.go        # Rule loader, glob matching, context injection
│   │   │   └── types.go         # Rule schema, priority levels
│   │   └── tools/
│   │       ├── curator.go       # Tool selection: blueprint + rules + config → tool set
│   │       └── types.go         # Tool scope definitions
│   ├── services/
│   │   ├── planner/
│   │   │   ├── planner.go       # Planning session management
│   │   │   └── decompose.go     # Plan structure + validation
│   │   ├── dispatch/
│   │   │   ├── dispatcher.go    # Plan → agent team spawning
│   │   │   └── scheduler.go     # Stream dependency scheduling
│   │   ├── mail/
│   │   │   ├── broker.go        # Mail routing + broadcast
│   │   │   ├── store.go         # SQLite mail persistence
│   │   │   └── types.go         # Message types + schemas
│   │   ├── merge/
│   │   │   ├── queue.go         # FIFO merge queue
│   │   │   ├── resolver.go      # Tiered conflict resolution
│   │   │   └── gates.go         # Quality gate execution
│   │   ├── lifecycle/
│   │   │   ├── manager.go       # Agent session lifecycle
│   │   │   └── watchdog.go      # Health monitoring
│   │   ├── events/
│   │   │   ├── bus.go           # Typed pub/sub event bus
│   │   │   └── store.go         # Event persistence
│   │   └── agents/
│   │       ├── registry.go      # Agent registration + state
│   │       ├── overlay.go       # System prompt overlay generation
│   │       └── roles.go         # Role definitions + constraints
│   ├── tui/
│   │   ├── app.go               # Root Bubble Tea model
│   │   ├── dashboard.go         # Dashboard view
│   │   ├── session.go           # Agent session view (sandbox PTY)
│   │   ├── plan.go              # Plan review/edit view
│   │   ├── diff.go              # Diff view with hunk actions
│   │   ├── merge.go             # Merge queue view
│   │   ├── log.go               # Live event log view
│   │   ├── keymap.go            # Keybinding definitions
│   │   └── mode.go              # Modal state (normal/insert)
│   ├── openclaw/
│   │   └── adapter.go           # OpenClaw agent adapter
│   ├── db/
│   │   ├── migrations/          # SQLite migrations
│   │   └── queries/             # SQL queries
│   └── config/
│       └── config.go            # Global + project config
├── extension/
│   └── deck-pi-extension/       # Pi extension (TypeScript, deployed to sandboxes)
│       ├── index.ts             # Extension entry point
│       ├── hooks.ts             # Mail injection, scope enforcement
│       ├── tools.ts             # deck.mail.send, deck.done, etc.
│       └── package.json
├── configs/
│   ├── defaults/
│   │   └── config.yaml          # Default daemon config
│   └── roles/
│       ├── planner.md           # Planner agent base definition
│       ├── lead.md              # Lead agent base definition
│       ├── scout.md             # Scout agent base definition
│       ├── builder.md           # Builder agent base definition
│       ├── reviewer.md          # Reviewer agent base definition
│       └── merger.md            # Merger agent base definition
├── go.mod
├── go.sum
└── README.md
```

---

## Configuration

### Daemon Config (`~/.config/deck/config.yaml`)

```yaml
daemon:
  listen: "0.0.0.0:9800"          # HTTP listen address
  data_dir: "~/.deck/data"        # SQLite databases, logs

sandbox:
  provider: "daytona"             # sandbox provider ("daytona", future: "docker", "e2b", "local")
  daytona:                        # Daytona-specific config (only when provider=daytona)
    api_key: "${DAYTONA_API_KEY}"
    default_snapshot: ""           # set after first deck snapshot create
  default_resources:              # provider-agnostic defaults
    cpu: 2
    memory: 4
    disk: 10
  auto_stop_interval: 60          # minutes
  auto_delete_interval: 180       # minutes

agents:
  runtime: "pi"                   # agent runtime ("pi", future: "claude-code", "codex")
  max_concurrent: 8               # max parallel agents
  max_depth: 2                    # hierarchy depth limit
  stagger_delay_ms: 2000          # delay between agent spawns
  idle_timeout_minutes: 30        # kill idle agents after this

planning:
  default_mode: "interactive"     # interactive | auto
  model: "claude-sonnet-4-6"      # model for planner agents

workers:
  model: "claude-sonnet-4-6"      # model for worker agents

merge:
  ai_resolve_enabled: true        # enable tier 3 (AI conflict resolution)
  max_ci_rounds: 2                # max test/fix cycles before escalation

watchdog:
  check_interval_seconds: 30
  nudge_after_minutes: 15
  escalate_after_nudges: 3

autonomy:
  default_level: 1                # 0=supervised, 1=guided, 2=monitored, 3=autonomous
  # Per-path and per-task-type overrides in project config

tools:
  max_per_agent: 15               # max tools exposed to any single agent
  always_include: []              # always available to all agents
  always_exclude: []              # never available to agents

quality_gates:                    # default gates, overridable per project
  - "bun test"
  - "bun run lint"

blueprints:
  dir: "~/.config/deck/blueprints"  # global custom blueprints
  # Project blueprints in .deck/blueprints/ take priority

rules:
  dir: "~/.config/deck/rules"      # global rules (apply to all projects)
  # Project rules in .deck/rules/ are additive

openclaw:
  enabled: false
  # When enabled, Deck registers as an OpenClaw agent
  # OpenClaw handles messaging, Deck handles orchestration
```

### Project Config (`.deck/config.yaml`)

```yaml
# Project-specific overrides
repo: "git@github.com:user/project.git"
default_branch: "main"
snapshot: "deck-myproject"          # sandbox snapshot for this project (provider-specific)

quality_gates:
  - "bun test"
  - "bun run lint"
  - "bun run typecheck"

autonomy:
  default_level: 1
  overrides:
    - scope: "src/payments/**"
      level: 0

tools:
  max_per_agent: 15
  always_include: ["mcp:filesystem:*"]

# Project-specific agent guidance (injected into all agent overlays)
guidance: |
  This project uses Bun, not npm.
  The auth module is in src/auth/.
  Tests use vitest, not jest.
```

### Project `.deck/` Directory Structure

```
.deck/
├── config.yaml              # project config (above)
├── blueprints/              # workflow definitions
│   ├── feature.yaml         # copied from defaults on deck init
│   ├── hotfix.yaml
│   ├── stream.yaml
│   └── migration.yaml       # user-defined custom blueprint
├── rules/                   # scoped rules (the compounding loop)
│   ├── auth-patterns.md
│   ├── testing.md
│   ├── payments.md
│   └── learned/             # auto-generated from human corrections (future)
│       └── 2026-03-01-fix-jwt-signing.md
└── sessions.yaml            # TUI session persistence
```

Everything in `.deck/` is version-controlled. Rules and blueprints are shared across the team. The harness improves with every commit.

---

## Implementation Phases

### Phase 1: Foundation
- Daemon skeleton with HTTP server + SSE event stream
- Sandbox provider interface + Daytona implementation
- Agent runtime interface + Pi implementation
- SQLite database layer (objectives, agents, mail, events)
- Event bus (pub/sub)
- Basic CLI client (deck plan, deck status)

### Phase 2: Harness Core
- Blueprint engine (YAML loader, state machine executor, shipped defaults)
- Scoped rules engine (glob matching, context injection)
- Tool curator (blueprint + rules + config → per-agent tool set)
- Quality gate runner (deterministic, sandbox-scoped)

### Phase 3: Planning
- Planner agent (interactive mode)
- Plan data model and lifecycle
- Plan approval flow (CLI-only initially)
- Objective lifecycle state machine
- Simple mode (single-agent escape hatch)

### Phase 4: Execution
- Blueprint execution (step-by-step state machine with gates)
- Agent role definitions and overlay generation (with rules + tools injection)
- Deck Pi extension (mail injection hooks, tools, scope enforcement)
- Lead → Worker spawning
- Mail broker (send, receive, broadcast, inject)
- Dependency-aware stream scheduling

### Phase 5: Merge & Review
- Merge queue (FIFO, tier 1-2 resolution)
- Quality gate execution in sandbox
- AI merge (tier 3)
- Diff data for client consumption

### Phase 6: TUI Client
- Dashboard view (objective/agent tree)
- Agent session view (PTY forwarding to sandbox)
- Plan review view
- Diff view with hunk actions
- Merge queue view
- Event log view

### Phase 7: Autonomy & Health
- Autonomy levels (config-driven, per-path overrides)
- Stuck detection (repeater, spinner, timeout)
- Watchdog (tier 0-1)
- Batch planning mode
- OpenClaw adapter

### Phase 8: Polish & Ecosystem
- Learned rules (auto-capture from human corrections)
- Additional agent runtimes (Claude Code, Codex CLI)
- Additional sandbox providers (Docker, E2B)
- Daytona snapshot management
- Cost tracking and reporting

---

## What Deck Is Not

- **Not an agent framework.** Deck doesn't implement agents. Agent runtimes (Pi, Claude Code, Codex) do the thinking. Deck orchestrates them.
- **Not a sandbox provider.** Daytona, Docker, E2B provide sandboxes. Deck manages their lifecycle through a provider interface.
- **Not a messaging platform.** OpenClaw handles messaging. Deck is reachable through it.
- **Not an IDE.** Deck doesn't edit code. Agents edit code. You review their work.
- **Not a CI system.** Deck runs quality gates locally in sandboxes. CI is your existing pipeline.
- **Not locked to any model or provider.** Bring your own agent runtime, sandbox provider, and model. The harness stays the same.

Deck is a **harness engineering framework** — the open-source, configurable infrastructure that makes AI coding agents reliable. It sits above agent runtimes, sandbox providers, and messaging platforms, coordinating all of them into a coherent workflow. Users configure blueprints for their workflows, write scoped rules for their conventions, plug in their preferred agent runtime and sandbox provider, and set their autonomy level.

The harness is the product. Users bring the horse.

---

## Summary

Deck is an open-source harness engineering framework that transforms a solo developer into a team lead. You think, plan, and review. Agents explore, implement, and test. Deck manages the machinery in between — spawning sandboxes, routing communication, enforcing quality gates, resolving conflicts, and keeping you informed from anywhere.

**What makes Deck different from every other tool in this space:**

1. **The harness is the product, not the model.** OpenAI and Stripe built proprietary harnesses for themselves. Deck is the configurable harness framework anyone can use.
2. **Daemon-first with pluggable everything.** No other tool offers a persistent headless orchestrator with pluggable agent runtimes, sandbox providers, and configurable blueprints.
3. **Scoped rules that compound.** Every mistake becomes a version-controlled rule. The harness gets smarter with every task.
4. **Progressive autonomy.** From fully supervised to fully autonomous, configured per project, per path, per task type.
5. **Simple mode to full orchestration.** Works as a single-agent harness on day one. Unlocks multi-agent coordination when you need it.

The architecture combines the best patterns from the field: Stripe's deterministic blueprints, Overstory's hook-injected communication, OpenClaw's hierarchical agent model, OpenCode's clean service architecture, Daytona's elastic sandboxes, and the emerging harness engineering discipline from OpenAI and the broader community. It avoids the weaknesses of each: no reliance on agents voluntarily communicating, no single-threaded human bottleneck, no uncontrolled agent spawning, no manual environment management, no lock-in to a single model or provider.

One objective in. Reviewed, tested, merged code out.
