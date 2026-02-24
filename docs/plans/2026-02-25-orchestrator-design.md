# Deck — Agentic Workflow Orchestrator Design

**Date:** 2026-02-25
**Status:** Draft — Under Review
**Author:** SynDG + Claude
**Prerequisite:** [MVP Design](2026-02-16-deck-design.md)

---

## Evolution

The MVP defined Deck as a terminal-native TUI for embedding agent sessions, reviewing diffs, and running commands. This document evolves Deck into a **daemon-first agentic workflow orchestrator** — a system that manages the full lifecycle of AI-assisted coding work: **plan, execute, review, merge**.

The MVP's TUI becomes one client interface into a headless orchestration engine that runs anywhere.

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

### 6. Blueprints (Deterministic + Agentic Workflow)

Inspired by Stripe's blueprint pattern: a state machine that interleaves deterministic code steps with agentic (LLM-driven) steps.

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

### 8. Watchdog & Health Monitoring

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

### 9. Event Bus

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

## Pi Agent Integration

Each agent is a Pi instance running in RPC mode inside a Daytona sandbox.

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
│   ├── services/
│   │   ├── planner/
│   │   │   ├── planner.go       # Planning session management
│   │   │   └── decompose.go     # Plan structure + validation
│   │   ├── dispatch/
│   │   │   ├── dispatcher.go    # Plan → agent team spawning
│   │   │   ├── blueprint.go     # Blueprint state machine
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
│   ├── pi/
│   │   ├── rpc.go               # Pi RPC client (stdin/stdout JSON)
│   │   └── hooks.go             # Hook definitions (mail inject, scope enforce)
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
  runtime: "pi"                   # agent runtime (pi is the only supported runtime for now)
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

quality_gates:                    # default gates, overridable per project
  - "bun test"
  - "bun run lint"

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

# Project-specific agent guidance (injected into all agent overlays)
guidance: |
  This project uses Bun, not npm.
  The auth module is in src/auth/.
  Tests use vitest, not jest.
```

---

## Implementation Phases

### Phase 1: Foundation
- Daemon skeleton with HTTP server + SSE event stream
- Sandbox provider interface + Daytona implementation
- SQLite database layer (objectives, agents, mail, events)
- Event bus (pub/sub)
- Pi RPC client (spawn Pi in sandbox, send prompts, receive responses)
- Basic CLI client (deck plan, deck status)

### Phase 2: Planning
- Planner agent (interactive mode)
- Plan data model and lifecycle
- Plan approval flow (CLI-only initially)
- Objective lifecycle state machine

### Phase 3: Execution
- Blueprint state machine (stream dispatching)
- Agent role definitions and overlay generation
- Deck Pi extension (mail injection hooks, tools, scope enforcement)
- Lead → Worker spawning
- Mail broker (send, receive, broadcast, inject)
- Dependency-aware stream scheduling

### Phase 4: Merge & Review
- Merge queue (FIFO, tier 1-2 resolution)
- Quality gate execution in sandbox
- AI merge (tier 3)
- Diff data for client consumption

### Phase 5: TUI Client
- Dashboard view (objective/agent tree)
- Agent session view (PTY forwarding to sandbox)
- Plan review view
- Diff view with hunk actions
- Merge queue view
- Event log view

### Phase 6: Health & Polish
- Watchdog (tier 0-1)
- Batch planning mode
- OpenClaw adapter
- Daytona snapshot management
- Cost tracking and reporting

---

## What Deck Is Not

- **Not an agent framework.** Pi is the agent runtime. Deck orchestrates Pi instances.
- **Not a sandbox provider.** Daytona provides sandboxes. Deck manages their lifecycle.
- **Not a messaging platform.** OpenClaw handles messaging. Deck is reachable through it.
- **Not an IDE.** Deck doesn't edit code. Agents edit code. You review their work.
- **Not a CI system.** Deck runs quality gates locally in sandboxes. CI is your existing pipeline.

Deck is the **orchestration layer** — it sits above agent runtimes, sandbox providers, and messaging platforms, coordinating all of them into a coherent workflow that turns your intent into merged, reviewed code.

---

## Summary

Deck transforms a solo developer into a team lead. You think, plan, and review. Agents explore, implement, and test. Deck manages the machinery in between — spawning sandboxes, routing communication, resolving conflicts, and keeping you informed from anywhere.

The architecture combines the best patterns from five different agent systems: Stripe's deterministic blueprints, Overstory's hook-injected communication, OpenClaw's hierarchical agent model, OpenCode's clean service architecture, and Daytona's elastic sandboxes. It avoids the weaknesses of each: no reliance on agents voluntarily communicating, no single-threaded human bottleneck, no uncontrolled agent spawning, no manual environment management.

One objective in. Reviewed, tested, merged code out.
