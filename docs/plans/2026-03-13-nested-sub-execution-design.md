# Nested Sub-Execution Design

**Date:** 2026-03-13
**Status:** Approved
**Context:** Phase 4 gap — stream blueprint sub-execution was never implemented. Leads were spawned as plain agents instead of orchestrating the stream blueprint steps.

---

## Problem

The feature blueprint's `blueprint_ref` step references `stream.yaml`, but `HandleBlueprintRefStep` spawns lead agents that execute a task description and exit. They don't run the stream blueprint's internal steps (scout → build → lint → review → merge_ready). This means:

- Quality gates don't run between build and review
- Fix-loops (lint failure → re-run builder) never trigger
- Scout and reviewer agents never spawn
- The stream blueprint is dead code from the coordinator's perspective

## Decision

**Coordinator-driven sub-execution (Option A).** The coordinator creates a sub-execution of `stream.yaml` per stream and advances it step-by-step using the existing `Engine.Advance()`. The lead agent role is removed from the core loop — the engine IS the orchestrator.

### Why not lead agents?

The blueprint engine already handles agent spawning, quality gates, fix-loops, and deterministic actions deterministically. Having an LLM re-discover "run lint after build, retry on fail" is wasteful and fragile. The design doc's "persistent lead" pattern maps to a persistent `Execution` record, not a persistent agent process.

### Multi-agent system preserved

Option A removes the lead role but keeps the full multi-agent system:

- **Across streams:** Multiple streams run in parallel, each with their own agents
- **Within a stream:** Scout, builder, and reviewer are separate agents spawned sequentially
- **Communication:** All agents share the mail system (`@builders`, `@all`, `@stream:{id}`)
- **Fix-loop:** Lint failure re-spawns builder with error context (up to N iterations)

For a 3-stream feature: up to 9 agent sessions (3 scouts + 3 builders + 3 reviewers).

---

## Design

### 1. Nested Sub-Execution Mechanism

`HandleBlueprintRefStep` creates one sub-execution per stream of the referenced blueprint, then advances them concurrently:

```
HandleBlueprintRefStep revised flow:

1. Load the referenced blueprint (step.Ref → registry.Get)
2. Get all streams for the plan
3. Subscribe to events
4. For each ready stream:
   a. engine.Start(streamBlueprint, objectiveID) → sub-execution
   b. Store sub-execution ID on the stream record
   c. Launch goroutine: advanceSubExecution(ctx, subExec, stream)
5. Event loop: wait for sub-execution completions
   - On EventStreamReady (cascade): start new sub-executions
   - On completion: track count
   - On failure: handle per on_stream_failure config
6. When all streams resolve → return StepResult
```

Each sub-execution is a full `Execution` record in the DB with its own `step_states`, `current_step`, and `status`. No new engine code needed — `Engine.Advance()` already handles agent steps, deterministic steps, retries, and fix-loops.

### 2. Sub-Execution Lifecycle & Concurrency

Each stream's sub-execution runs in its own goroutine:

```go
func (c *Coordinator) advanceSubExecution(
    ctx context.Context,
    execID string,
    stream *domain.Stream,
) error {
    for {
        exec, err := c.engine.Advance(ctx, execID)
        if err != nil {
            return err
        }
        switch exec.Status {
        case "completed":
            c.scheduler.MarkCompleted(ctx, stream.ID, stream.PlanID)
            return nil
        case "failed":
            c.scheduler.MarkFailed(ctx, stream.ID)
            return fmt.Errorf("stream %s failed at step %s: %s",
                stream.ID, exec.CurrentStep,
                exec.StepStates[exec.CurrentStep].Error)
        case "waiting_human":
            return fmt.Errorf("human step in sub-execution not yet supported")
        }
    }
}
```

**Concurrency model:**

- Independent streams run in parallel goroutines (up to `scheduler.maxConcurrent`)
- Dependent streams start when dependencies complete (cascade via `EventStreamReady`)
- Each goroutine reports via a results channel
- Parent event loop collects results and spawns newly ready streams

### 3. Failure Model

A single stream failure does NOT kill the entire objective. Independent streams continue.

```
Stream 1: auth middleware  [deps: none] ✅ completed
Stream 2: user service     [deps: none] ❌ builder failed lint 3x
Stream 3: API routes       [deps: 1, 2] ⏳ cancelled (depends on 2)
```

**Escalation chain per stream:**

1. Gate fails → fix-loop re-spawns builder with error context (up to `max_fix_iterations`)
2. Fix-loop exhausted → check `on_stream_failure` config
3. If `escalate`: mail `@human` with structured report, pause stream, other streams continue
4. If `fail`: mark stream failed, other streams continue

**After all streams resolve:**

- All completed → objective `completed`
- Some failed → objective `partial` (new status)
- Streams awaiting human → objective stays `executing`

Only streams that reach `merge_ready` enter the merge queue. Failed streams never merge.

### 4. Human Escalation

When escalating to `@human`, send a structured report with everything the human needs to respond:

```json
{
  "stream": "Update auth middleware",
  "file_scope": ["src/auth/**"],
  "failed_step": "lint",
  "gate_command": "go vet ./...",
  "exit_code": 1,
  "stderr": "cannot use *AuthStruct as auth.Context...",
  "fix_attempts": [
    {"iteration": 1, "agent_summary": "Tried wrapping with interface cast", "result": "same error"},
    {"iteration": 2, "agent_summary": "Replaced struct with interface", "result": "new error: missing method"},
    {"iteration": 3, "agent_summary": "Added missing methods", "result": "same original error"}
  ],
  "files_changed": ["src/auth/middleware.go", "src/auth/context.go"],
  "question": "How should the agent resolve this?"
}
```

### 5. Human Retry with Guidance

The human responds with guidance that gets injected into the agent's fix context:

```
POST /executions/{id}/retry
{
  "guidance": "The auth middleware expects a Context interface, not a concrete struct. Use auth.NewContext() wrapper instead."
}
```

The builder overlay receives both the original error AND the human's guidance:

```markdown
## Fix Context

Previous error (after 3 automated fix attempts):
lint: `go vet ./...` failed with exit code 1
stderr: cannot use *AuthStruct as auth.Context...

Human guidance:
The auth middleware expects a Context interface, not a concrete struct.
Use auth.NewContext() wrapper instead.
```

If no guidance is provided (e.g., the fix was external — updated a dependency, fixed config), the stream retries with just the original error context.

CLI: `deck retry <execution-id> --guidance "use auth.NewContext() wrapper"`

### 6. Escalation Configuration

Configurable on the `blueprint_ref` step:

```yaml
- id: per_stream
  type: blueprint_ref
  ref: stream.yaml
  on_stream_failure: escalate    # "escalate" | "fail"
  escalation:
    context: full                # "full" | "minimal" | "error_only"
    include_agent_history: true
    channel: default             # "default" (mail) | future: "discord", "slack"
    prompt: "How should the agent resolve this?"
  next: merge
```

**Context levels:**

- `full` (default) — gate output, all fix attempt summaries, files changed, diff
- `minimal` — failed step, error message, iteration count
- `error_only` — just the stderr/stdout

### 7. Data Model Changes

**Execution struct — two new fields:**

```go
type Execution struct {
    // ... existing fields ...
    ParentID string  // parent execution ID (empty for top-level)
    StreamID string  // which stream this sub-execution drives (empty for top-level)
}
```

**Stream struct — one new field:**

```go
type Stream struct {
    // ... existing fields ...
    ExecutionID string  // sub-execution driving this stream
}
```

**Schema:** two new columns on `executions` table, one on `streams` table.

**New objective status:** `partial` — valid transition from `executing`. Means "some work merged, check failed streams."

### 8. Blueprint Schema Changes

New fields on `blueprint_ref` step type:

```go
type Step struct {
    // ... existing fields ...
    OnStreamFailure string           `yaml:"on_stream_failure"` // "escalate" | "fail"
    Escalation      *EscalationConfig `yaml:"escalation"`
}

type EscalationConfig struct {
    Context             string `yaml:"context"`               // "full" | "minimal" | "error_only"
    IncludeAgentHistory bool   `yaml:"include_agent_history"`
    Channel             string `yaml:"channel"`               // "default" | future channels
    Prompt              string `yaml:"prompt"`
}
```

---

## Full Flow Example

```
User: "Refactor auth system to use JWT tokens"

Feature Blueprint:
  1. PLAN (agent/planner)
     → Decomposes into 3 streams with dependencies

  2. APPROVE (human)
     → User reviews plan

  3. DISPATCH (deterministic)
     → Scheduler resolves dependencies

  4. PER_STREAM (blueprint_ref → nested sub-executions)
     ┌─────────────────────┐  ┌─────────────────────┐
     │ Stream 1 (parallel)  │  │ Stream 2 (parallel)  │
     │ scout agent          │  │ scout agent          │
     │ builder agent        │  │ builder agent        │
     │ lint (gates)         │  │ lint (gates)         │
     │  └→ fix-loop if fail │  │  └→ fix-loop if fail │
     │ reviewer agent       │  │ reviewer agent       │
     │ merge_ready          │  │ merge_ready          │
     └──────────┬───────────┘  └──────────┬───────────┘
                └──── both done ──────────┘
                         ↓ cascade
     ┌─────────────────────────────────────┐
     │ Stream 3 (was blocked on 1 & 2)     │
     │ scout → build → lint → review →     │
     │ merge_ready                          │
     └─────────────────────────────────────┘

  5. MERGE (deterministic)
     → Merge queue processes merge_ready streams only

  6. COMPLETE
     → Objective: completed | partial
```

---

## Scope of Changes

### New code

- `advanceSubExecution()` — goroutine loop driving a stream's sub-execution
- `buildEscalationPayload()` — collects fix attempt history, gate output, files changed
- `POST /executions/{id}/retry` — daemon route accepting optional `guidance` field
- `partial` objective status constant + lifecycle transition
- `EscalationConfig` struct and blueprint validation

### Modified code

- `HandleBlueprintRefStep` — create sub-executions instead of spawning leads
- `Execution` struct — add `ParentID`, `StreamID`
- `Stream` struct — add `ExecutionID`
- `executions` table — two new columns
- `streams` table — one new column
- `Step` struct — add `OnStreamFailure`, `Escalation` fields
- Blueprint loader/validator — validate new fields
- `feature.yaml` — add `on_stream_failure: escalate` default

### Removed code

- Lead agent spawning from `HandleBlueprintRefStep`

### Unchanged

- `Engine.Advance()` — works as-is for sub-executions
- `HandleAgentStep` — spawns scout/builder/reviewer the same way
- `HandleDeterministic` — quality gates, merge_ready unchanged
- Mail system — escalation uses existing `@human` broadcast
- Scheduler — dependency cascade unchanged
