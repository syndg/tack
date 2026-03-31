# Observability, Partial Completion & Timeouts

**Date:** 2026-03-14
**Status:** Approved
**Context:** After two test runs on the splitwise project, several gaps prevent tack from running unsupervised: no visibility into agent activity, partial stream failures stall the whole pipeline, no timeout enforcement, and manual merging required.

---

## Summary

Five changes that make tack operational without constant human intervention:

1. **Agent activity events** — pipe process events to event bus + JSONL log files
2. **`tack watch` / `tack logs`** — CLI commands for live monitoring and replay
3. **Partial completion** — merge succeeded streams, skip failed ones, mark objective `partial`
4. **Configurable timeouts** — per-role max duration and idle timeout
5. **Base branch config** — configurable PR target branch

---

## 1. Agent Activity Events

### Problem

Agent process events (tool calls, messages, errors) are consumed by process handlers and discarded. The event bus only receives lifecycle events (spawn, complete, fail). No way to know what an agent is doing.

### Design

Publish agent activity through two channels:

**In-memory (event bus):** New event type `agent.activity` published from Pi/Claude-code process handlers as events arrive. No buffering. Enables live streaming via SSE `/events` endpoint.

**Persistent (JSONL files):** Per-agent log file at `.tack/data/logs/{agent-id}.jsonl`. Append-only, one JSON object per line. Files cleaned up when sandbox is removed.

Activity payload structure:

```go
type AgentActivityPayload struct {
    Kind     string `json:"kind"`      // "tool_start", "tool_end", "message", "thinking"
    Tool     string `json:"tool"`      // tool name (empty for message/thinking)
    Content  string `json:"content"`   // tool args, message text, or truncated output
    Duration int    `json:"duration"`  // ms, only on tool_end
    IsError  bool   `json:"is_error"`  // tool failed
}
```

**Why not SQLite for activity:** Activity events are high-volume (hundreds per agent session), append-heavy, and disposable. JSONL files are naturally scoped per agent, cheap to write, and easy to clean up. SQLite is reserved for structured data that needs querying (objectives, plans, streams, escalations).

### Implementation

- `PiProcess.handleEvent()`: on `tool_execution_start`, `tool_execution_end`, `message_update` — publish `agent.activity` event to bus + append to JSONL log
- `ClaudeCodeProcess`: parse stdout for tool patterns, publish similarly
- New `internal/services/agents/activity.go`: `ActivityLogger` struct that manages JSONL file handles, provides `Log(agentID, payload)` and `Read(agentID) io.Reader`
- Log dir created at daemon startup: `{data_dir}/logs/`

---

## 2. CLI Commands: `tack watch` and `tack logs`

### `tack watch`

Subscribes to SSE `/events` endpoint. Renders a compact streaming log.

Three verbosity levels:

| Flag | Level | Shows |
|---|---|---|
| `--summary` | summary | Objective status changes only |
| *(default)* | normal | Above + stream transitions, agent spawn/complete, gate results, merge events |
| `--verbose` | verbose | Above + every tool call, file read/write, message content |

Filters:

- `tack watch --stream <id>` — single stream
- `tack watch --agent <id>` — single agent
- `tack watch --objective <id>` — single objective

Output format:

```
16:01:20 [backend-fixes] builder spawned
16:01:35 [backend-fixes] builder: Read backend/src/types/index.test.ts
16:01:38 [backend-fixes] builder: Edit backend/src/types/index.test.ts
16:01:42 [backend-fixes] builder: bash bun test (1.2s)
16:01:45 [backend-fixes] lint passed (file-attribution: 0 in-scope errors)
16:01:47 [backend-fixes] reviewer spawned
16:02:10 [backend-fixes] reviewer completed
16:02:10 [backend-fixes] → merge_ready
16:02:12 [backend-fixes] merged ✓
```

### `tack logs <agent-id>`

Reads the JSONL file from `.tack/data/logs/{agent-id}.jsonl`. Same verbosity flags. If the agent is still running, tails the file live.

### Implementation

- `cmd/tack/watch.go`: new cobra command, connects to SSE, formats events
- `cmd/tack/logs.go`: new cobra command, reads JSONL, supports `--follow` for live tailing
- SSE endpoint `/events` already exists — no changes needed to the daemon

---

## 3. Partial Completion

### Problem

`merge_queue` step collects all `merge_ready` streams and blocks until every stream resolves. If any stream fails, it never reaches `merge_ready`, so `merge_queue` waits forever. The objective stalls and requires manual intervention.

### Design

`merge_queue` partitions streams into three buckets:

1. **`merge_ready`** — merge these now
2. **`failed`** — skip, already terminal
3. **`executing` / `pending`** — still running, wait for them

When no streams are `executing` or `pending`, proceed with whatever is `merge_ready`. The merge step processes available streams. `mark_complete` then sets the objective status:

- All streams merged → `completed`
- Some streams failed → `partial` (status already exists in codebase)
- All streams failed → `failed`

The `create_pr` step runs regardless (for `completed` or `partial`). The PR body includes a summary of which streams succeeded and which failed.

### Implementation

Changes to `internal/services/dispatch/handlers.go`:

- `mergeQueue()`: instead of collecting only `merge_ready`, also check for `failed` streams. When `pending + executing == 0`, proceed with available `merge_ready` streams.
- `createPR()`: include stream status summary in PR body.

No new config. No threshold.

---

## 4. Configurable Timeouts

### Problem

No timeout on agent execution. `idle_timeout_minutes` config exists but isn't enforced. Agents can run forever (observed: reviewer stuck for 23+ minutes with no process running).

### Design

Two timeout types, configurable per role:

```yaml
agents:
  timeouts:
    default:
      max_duration_minutes: 30
      idle_minutes: 10
    planner:
      max_duration_minutes: 15
      idle_minutes: 5
    builder:
      max_duration_minutes: 45
      idle_minutes: 10
    reviewer:
      max_duration_minutes: 15
      idle_minutes: 5
```

**Max duration:** hard wall clock limit. Agent killed after this regardless of activity. Implemented as `time.AfterFunc` started at agent spawn.

**Idle timeout:** kill if no activity events for N minutes. Resets on every `agent.activity` event from the event bus. Requires activity events flowing through the bus (Design 1).

When either timeout fires:
1. Kill the agent process
2. Mark agent session as failed with reason: `"max duration exceeded (45m)"` or `"idle timeout (10m no activity)"`
3. Trigger normal fix-loop or escalation flow

### Implementation

- `internal/config/config.go`: add `TimeoutConfig` struct with per-role overrides
- `internal/services/dispatch/coordinator.go`: start timeout timers on agent spawn, cancel on agent completion, reset idle timer on activity events
- Coordinator subscribes to `agent.activity` events to reset idle timers

---

## 5. Base Branch Config

### Problem

`create_pr` doesn't specify a PR base branch. `gh pr create` infers from repo default, which may not be correct.

### Design

```yaml
daemon:
  base_branch: "main"    # default: "main"
```

### Implementation

- `internal/config/config.go`: add `BaseBranch` field to `DaemonConfig`
- `internal/services/dispatch/handlers.go`: `createPR()` passes `--base {baseBranch}` to `gh pr create`

---

## Implementation Order

1. **Agent activity events + JSONL logging** — foundation for everything else
2. **Configurable timeouts** — depends on activity events for idle reset
3. **`tack watch` + `tack logs`** — CLI consumers of the event stream
4. **Partial completion** — independent, can be done in parallel
5. **Base branch config** — trivial, do alongside anything
