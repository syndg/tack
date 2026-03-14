# Inter-Agent Communication Layer

> Runtime-agnostic protocol with Pi-native reference adapter

## Architecture

Deck's inter-agent communication splits into two layers:

**Protocol layer** (daemon, Go) — the stable contract that all adapters talk to. Owns message storage, addressing, group resolution, role-based filtering, and read-tracking. Exposed as HTTP endpoints. Runtime-agnostic — doesn't know or care whether agents run on Pi, Claude Code, or anything else.

**Adapter layer** (per-runtime) — thin bindings that wire the protocol to a runtime's native lifecycle events. Each adapter handles three concerns only: (1) _when_ to check for mail (lifecycle binding), (2) _how_ to deliver it (injection/presentation), (3) _how_ to send it (tool registration). Adapters have zero domain logic — no filtering, no group resolution, no protocol awareness beyond "call these HTTP endpoints."

The Pi adapter is the reference implementation and the richest. A Claude Code adapter would be thinner — shell hooks with debounce. Both talk to the same daemon API.

## Message Model

```
MailMessage {
  id          string        // assigned by daemon
  from        string        // sender agent name
  to          string        // recipient or group (@builders, @stream:{id}, @all, @human)
  subject     string        // short summary for triage
  body        string        // full message content
  type        enum          // message | status | dispatch | worker_done | merge_ready | escalation | question
  priority    enum          // low | normal | high | urgent
  threadId    string?       // null for new conversations, set for replies
  payload     string?       // structured JSON for protocol messages
  read        bool          // read-tracking
  objective   string        // objective ID (required, scoping)
  stream      string?       // stream ID (optional scoping)
  createdAt   timestamp
}
```

- `subject` exists for triage — agents scan subjects without parsing full bodies.
- `type` is a closed enum validated by the daemon. Adding a new type is a protocol version change.
- `threadId` enables reply chains for question/answer flows between agents.
- `objective` is required — all messages scoped to an objective. No cross-objective chatter.
- `payload` is typed by `type` — e.g., `worker_done` carries `{summary, branch, files_changed}`.

## Daemon Protocol (HTTP API)

### Sending

```
POST /mail
Body: { from, to, subject, body, type, priority?, threadId?, payload?, objective, stream? }
Response: { id }
```

Group addresses (`@builders`, `@stream:{id}`, `@all`) resolved server-side. The daemon fans out to individual recipients. `@human` publishes an event instead of delivering to an inbox.

### Receiving (filtered)

```
GET /mail/{agentName}/unread?role={role}&stream={streamId}
Response: [ MailMessage, ... ]
```

The daemon applies the relevance matrix based on `role`. Stream param scopes results further. Messages ordered by priority (urgent first) then creation time.

### Read-tracking

```
POST /mail/{agentName}/read-all
POST /mail/{id}/read
```

Adapters call `read-all` after injection. Individual `read` exists for partial consumption.

### Replying

```
POST /mail/{id}/reply
Body: { from, body }
```

Daemon auto-sets `threadId`, `to` (original sender), `subject` (Re: original), `type` (inherited), `objective`, `stream`.

### Listing (debug/dashboard)

```
GET /mail?objective={id}&from={name}&to={name}&type={type}&unread=true
```

No filtering applied — returns everything matching query params.

## Relevance Matrix

Server-side filtering applied when `?role=` is passed to the unread endpoint.

```
              planner  scout  builder  reviewer  merger  lead
message          Y       Y       Y        Y        Y      Y
status           -       -       -        -        -      Y
dispatch         -       -       Y        -        -      Y
worker_done      -       -       -        -        -      Y
merge_ready      -       -       -        -        Y      Y
escalation       -       -       -        -        -      Y
question         Y       Y       Y        Y        Y      Y
```

- `message` and `question` pass through to everyone — direct communication should always arrive.
- `status` only reaches `lead` — coordination overhead, not for workers.
- `dispatch` reaches `builder` and `lead` — work assignments.
- `worker_done` and `escalation` only reach `lead` — upward signals.
- `merge_ready` reaches `merger` and `lead`.

The matrix is a Go map, not config. Adding a new type or role means a code change and a test.

When `?role=` is omitted, no filtering — all unread messages returned. Escape hatch for debug dashboards and custom adapters.

## Pi Adapter — Lifecycle Binding

| Lifecycle event | Action | Details |
|---|---|---|
| `session_start` | Initial mail fetch | Inject all unread as system context. Agent starts with full awareness. |
| `before_agent_start` | Per-prompt check | Primary delivery point. Fires once per prompt (not every LLM turn). |
| `turn_end` | Debounced check (10s) | Only if turn took >2s. Urgent → `sendMessage` with `steer`. Normal → `sendMessage` with `nextTurn`. |

All injection points call `POST /mail/{name}/read-all` immediately after fetching. No message is ever injected twice.

### Presentation format

```
--- You have 2 new messages ---
[URGENT] From: lead-a7a5 (dispatch): Build the validation schemas
  Subject: Stream 1 assignment
  Reply with: deck_mail_send to="lead-a7a5" threadId="msg-abc123"

From: scout-a7a5 (status): Found 3 route files needing validation
  Subject: Scout report
---
```

Includes reply instructions so the agent knows how to respond in-thread.

## Pi Adapter — Tools

Three tools registered via `pi.registerTool()`:

**`deck_mail_send`** — General-purpose messaging. Parameters: `to`, `subject`, `body`, `type?`, `priority?`, `threadId?`. Auto-populates `from`, `objective`, `stream` from env vars.

**`deck_escalate`** — Convenience wrapper. Parameters: `reason`, `to?` (defaults `@human`). Auto-sets `type: "escalation"`, `priority: "high"`.

**`deck_done`** — Task completion signal. Parameters: `summary`. Sends `worker_done` to `@lead` with structured payload. Also emits `DECK_DONE:` via Pi notify for Go process handler.

## Claude Code Adapter (Reference Sketch)

Validates runtime-agnostic design. Not built now.

```
SessionStart:     deck mail check --inject --agent $DECK_AGENT_NAME
UserPromptSubmit: deck mail check --inject --agent $DECK_AGENT_NAME
PostToolUse:      deck mail check --inject --agent $DECK_AGENT_NAME --debounce 30000
```

`deck mail check --inject` is a CLI subcommand that calls the daemon API, formats messages as text, writes to stdout, and marks as read.

Tools registered as MCP tools or bash commands in the system prompt.

Same protocol, same daemon, same filtering. Thinner delivery mechanism.

## Migration Scope

### Daemon (Go) changes
- Extend `MailMessage` — add `subject`, `priority`, `threadId`, `payload` fields
- Add `POST /mail/{id}/reply` endpoint
- Add relevance matrix and `?role=&stream=` filtering to `GET /mail/{name}/unread`
- Add group resolution for `@builders`, `@scouts`, `@reviewers`, `@stream:{id}`, `@all`
- SQLite schema migration for new columns

### Pi extension (TypeScript) rewrite
- Kill `context` hook entirely
- Add `session_start` — initial mail fetch and injection
- Add `before_agent_start` — per-prompt mail check (keep planner YAML hook, add mail)
- Add `turn_end` — debounced check with priority-based delivery
- Rewrite tools: `deck_mail_send` (with subject, priority, threadId), `deck_escalate`, `deck_done`
- Remove `deck_status` (folded into `deck_mail_send`)
- Mark messages read after every injection

### No changes to
- Runtime interface (`runtime.go`)
- Spawner (env vars already set)
- Process handler (`process.go`)
- Merge processor, coordinator, blueprint engine

## Future: Push Delivery

The protocol supports adding push without breaking changes. The adapter opens an SSE connection (`GET /mail/{name}/stream`), daemon notifies on new arrivals, adapter still uses the same fetch + mark-read endpoints. Claude Code adapters stay pull-only. Each adapter uses what its runtime supports.
