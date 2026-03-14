# Gate Isolation, Escalation Dedup & Comm System Simplification

**Date:** 2026-03-14
**Status:** Approved
**Context:** Splitwise test run exposed three architecture issues when running 4 concurrent builders.

---

## Background

Test run: 4 builder agents working concurrently on isolated worktrees (2 backend, 2 frontend). All 4 streams failed. Root causes:

1. Quality gates run against the entire project. Stream A failed because of TypeScript errors in Stream B's files. Stream A couldn't fix files it didn't own, looped 3 times, died.
2. Each fix-loop iteration sent an identical `@human` escalation. 50 copies of the same message.
3. Inter-agent mail (broadcasts, relevance matrix, priority ordering) generated 122 messages. None were actionable — agents in isolated worktrees can't see or react to each other's changes.

## Decision: Inter-Agent Communication

With isolated worktrees, agents work in separate branches. Agent A changing an API signature has no effect on agent B's branch. Agent-to-agent messages are informational noise — the receiving agent cannot act on them.

Inter-agent coordination happens at **merge time** via the merge processor, not during execution via messaging.

**Keep:**
- Agent-to-human escalation (`@human` / `deck_escalate` tool)
- Human-to-agent steer (inject message into running agent)
- Agent-to-system done signal (`deck_done` tool)
- Mail storage layer (escalation history, audit trail)

**Strip:**
- Broadcast fanout to agents (`@builders`, `@all`, `@scouts`, `@reviewers`, `@leads`)
- Relevance matrix and role-based filtering in `Broker.GetUnread`
- Priority-based ordering for agent mail delivery
- `deck_mail_send` tool (agent-to-agent messaging)
- `before_agent_start` mail polling in Pi extension
- `turn_end` debounced mail polling in Pi extension

**Broadcast addresses after simplification:**
- `@human` — the only broadcast target. Triggers `EventEscalation`.

---

## Design 1: File-Attribution Gates

### Problem

Quality gates (`bunx tsc --noEmit`, `bun test`, `bun run lint`) run against the entire project. A stream fails when errors exist in files outside its `file_scope`. The stream cannot fix those errors, exhausts fix iterations, and dies.

### Solution

Run the full gate command (unchanged). If the gate passes, done. If the gate fails, extract file paths from the error output and check whether any erroring file falls within the stream's `file_scope`. If none do, the gate passes anyway. If any do, the gate fails and only the relevant errors are fed into the fix-loop context.

### Error File Extraction

Most tools prefix errors with file paths:

```
src/routes/expenses.ts(76,9): error TS2345: ...
frontend/src/components/ExpenseForm.tsx: 45:14  warning  ...
FAIL src/routes/users.test.ts > POST /users > validates input
```

The gate runner parses stderr/stdout line-by-line, extracts file paths via regex, and collects unique erroring files. Pattern: match lines starting with a relative or absolute file path followed by a delimiter (`:`, `(`, ` line `).

### File Scope Matching

The stream's `file_scope` contains glob patterns (e.g., `["backend/src/routes/**", "backend/src/types/index.ts"]`). Each erroring file is matched against these globs. If at least one erroring file matches, the gate fails for this stream.

### Cross-File Errors

If stream A modifies `types.ts` (in scope) causing a type error in `routes/users.ts` (out of scope), the error file is out of scope and the gate passes for stream A. This is intentional — the error is caught at merge time when post-merge gates run against the full merged codebase. The merge processor already runs quality gates after merging each stream branch.

### Fix-Loop Error Filtering

When a gate fails (erroring files overlap with `file_scope`), the `fix_context` metadata passed to the re-spawned builder includes only the errors from in-scope files. This prevents the builder from seeing irrelevant errors it cannot fix.

### Implementation

Changes to `internal/harness/gates/`:

1. Add `ExtractErrorFiles(output string) []string` — regex-based file path extraction from gate output.
2. Add `FilterByScope(files []string, scope []string) []string` — glob matching of error files against stream file scope.
3. Modify `Runner.Run()` to accept an optional `fileScope []string`. When provided and a gate fails, apply file-attribution logic. When `fileScope` is empty (e.g., post-merge gates), fail on any error as before.

Changes to `internal/services/dispatch/handlers.go`:

4. `runQualityGates` passes the stream's `file_scope` to the gate runner when executing within a sub-execution (StreamID is set).

---

## Design 2: Escalation Dedup

### Problem

When a stream hits the same blocker on every fix-loop iteration, it sends an identical `@human` escalation each time. The test run produced 50 copies of the same TS error escalation.

### Solution

Before sending an escalation, compute a dedup key from `sha256(subject + stream_id)`. Check the mail store for an existing unread escalation with the same dedup key. If one exists, skip sending. If the previous escalation has been read (human has seen it), allow a new one through — the problem persists and the human should know.

### Implementation

Changes to `internal/domain/types.go`:

1. Add `DedupKey string` field to `MailMessage`.

Changes to `internal/db/mail.go`:

2. Add `dedupKey` column to the mail table (migration).
3. Add `ExistsUnread(ctx, dedupKey string) (bool, error)` — checks for an unread message with the given dedup key.

Changes to `internal/services/mail/broker.go`:

4. In `Send()`, when `msg.Type == "escalation"`, compute dedup key and call `ExistsUnread`. Skip if duplicate exists.

---

## Design 3: Comm System Simplification

### Strip from `internal/services/mail/broker.go`

- Remove `sendBroadcast` fanout for `@builders`, `@all`, `@scouts`, `@reviewers`, `@leads`
- Remove role parameter from `GetUnread`
- Remove relevance matrix filtering logic
- Keep `@human` broadcast (publishes `EventEscalation`, does not store mail)
- Keep `Send`, `Reply`, `MarkAllRead`, `List` for escalation storage

### Strip from `internal/domain/types.go`

- Remove `AgentRole` type and constants
- Remove `RelevanceMatrix` map
- Keep `MailType`, `MailPriority`, `MailMessage`

### Strip from `internal/runtime/pi/extension/index.ts`

- Remove `deck_mail_send` tool definition
- Remove `before_agent_start` mail polling hook
- Remove `turn_end` debounced mail check
- Keep `session_start` (initial escalation check — human may have steered)
- Keep `deck_escalate` tool
- Keep `deck_done` tool

### Strip from `internal/daemon/routes.go`

- Remove `?role=` query param from unread endpoint
- Keep all mail endpoints (useful for escalation history, debug)

### Strip from `internal/services/mail/broker_test.go`

- Remove `TestGetUnread_RelevanceMatrixFiltering`
- Remove `TestReply_CreatesThreadedMessage` (reply was for agent-to-agent threads)
- Update remaining tests to not pass role parameter

---

## Project Configuration

Standard project layout:

```
.deck/
  config.yaml          # runtime: model, provider, gates, concurrency
  blueprints/          # workflow: execution step definitions
    stream.yaml        # per-stream flow (build → lint → review → merge_ready)
  rules/               # context: project-specific agent guidance
    project.yaml
  data/                # managed by deck (sqlite, logs)
```

Deck ships default blueprints (`feature.yaml` top-level, `stream.yaml` per-stream). Projects override by placing their own versions in `.deck/blueprints/`. Config at `.deck/config.yaml` is the only required file.

---

## Implementation Order

1. **File-attribution gates** — unblocks streams from dying on unrelated errors
2. **Escalation dedup** — stops spam, small change
3. **Comm system strip** — cleanup, removes dead code
