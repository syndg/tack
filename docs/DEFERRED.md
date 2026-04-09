# Deferred Items

Items intentionally deferred from their original phase or parked for later design. Each entry tracks where it was originally scoped, why it was deferred, and when it should land.

For current product behavior, start with `docs-site/content/docs/` and `docs/README.md`. This file is the future-work backlog, not the current-state source of truth.

---

## From Phase 1 (Foundation)

### Daytona sandbox provider implementation
- **Original scope:** Design doc Phase 1 — "Sandbox provider interface + Daytona implementation"
- **What exists:** `internal/sandbox/provider.go`, `internal/sandbox/types.go` (interfaces only)
- **What's missing:** `internal/sandbox/daytona/` (concrete provider that talks to Daytona API)
- **Deferred to:** Phase 4 (Execution) — needed when agents actually spawn in sandboxes
- **Reason:** No Phase 2 or 3 code exercises a real sandbox. The interface is sufficient until execution.

### Pi runtime implementation
- **Original scope:** Design doc Phase 1 — "Agent runtime interface + Pi implementation"
- **What exists:** `internal/runtime/runtime.go` (interfaces only)
- **What's missing:** `internal/runtime/pi/` (Pi runtime, RPC client, hook definitions)
- **Deferred to:** Phase 4 (Execution) — needed when agents are actually spawned
- **Reason:** Same as above. The interface is sufficient for harness and planning phases.

### Config defaults drift from design doc
- **Details:** Several `internal/config/config.go` defaults diverge from design doc:
  - `Agents.Runtime` is `"claude-code"` (design doc says Pi)
  - `Planning.DefaultMode` is `"collaborative"` (design doc uses `"interactive"` / `"auto"`)
  - `Tools.MaxPerAgent` is `20` (design doc says `15`)
  - `QualityGates` defaults are `["lint", "test", "build"]` (design doc uses bun commands)
- **Deferred to:** When real providers are wired (Phase 4+) — align defaults to match actual implementations
- **Reason:** Cosmetic until providers exist. Will tune when we know which runtime/commands are real.

### Empty configs/defaults/ directory
- **Details:** `configs/defaults/` exists but has no shipped config file
- **Deferred to:** Phase 4+ — ship default config when real providers are configured
- **Reason:** No point shipping a default config until we know the real defaults.

### Single DB vs multi-DB
- **Details:** Implementation uses one `tack.db`. Design doc describes logical DBs (`objectives.db`, `agents.db`, etc.)
- **Deferred to:** Revisit if performance requires it. Likely never — single DB is simpler.
- **Reason:** Single SQLite file is correct for the current scale. Split only if needed.

---

## From Phase 2 (Harness Core)

_(none yet)_

---

## From Phase 3 (Planning)

### Automatic planner spawning for `planning_mode=batch`
- **Original scope:** Task 8.2 — `Auto bool` in `CreateObjectiveRequest` indicates batch planning mode
- **What exists:** Objectives now persist `planning_mode="batch"` when `auto=true`
- **What's missing:** A planner execution loop that reads `planning_mode` and actually auto-spawns/runs the planner agent
- **Deferred to:** Phase 4 (Execution) — wire planner spawning to the persisted mode
- **Reason:** Phase 3 now records the intent correctly, but there is still no runtime planner execution loop to act on it.

---

## From Phase 4 (Execution)

### EscalationConfig fields not wired
- **Original scope:** Phase 4 nested sub-execution — escalation on stream failure
- **What exists:** `EscalationConfig.IncludeAgentHistory` and `Channel` declared in `internal/config/config.go`, parse from YAML
- **What's missing:** Nothing reads these fields at runtime. The escalation path (`handlers.go` → mail broadcast) ignores both.
- **Deferred to:** Future phase — requires a design decision on delivery semantics
- **Reason:** Implementing requires deciding what `channel` means (Slack webhook? event bus topic?) and how `include_agent_history` attaches logs. Orthogonal to sub-execution / retry / merge correctness.

---

## From Phase 5 (Merge & Review)

### Merge tier 3 — AI conflict resolution
- **Original scope:** Phase 5 PRD — tiered merge strategy (clean → auto-resolve → AI)
- **What exists:** `internal/services/merge/git.go` tiers 1-2 fully working. Tier 3 logs a warning and returns failure.
- **What's missing:** An LLM-based conflict resolver that reads conflict markers and generates resolutions
- **Deferred to:** Future phase — needs LLM integration design
- **Reason:** Tiers 1-2 handle most cases. Real multi-stream features will eventually hit conflicts that `-X theirs` can't resolve safely, but this requires designing how the merger agent interacts with the merge processor.

### Watchdog service for idle/stuck agents
- **Original scope:** Config defines `watchdog.check_interval_seconds`, `nudge_after_minutes`, `escalate_after_nudges`
- **What exists:** Config fields in `internal/config/config.go` parse from YAML
- **What's missing:** No watchdog service exists. Idle or stuck agents are never detected, nudged, or escalated.
- **Deferred to:** Future phase — needs behavior spec
- **Reason:** Requires deciding: what counts as idle (no tool calls? no output?), what a "nudge" does (send mail? inject prompt?), and how escalation differs from the existing stream failure path.

### SSE per-client event filtering
- **Original scope:** `GET /events` SSE endpoint
- **What exists:** SSE handler broadcasts all events to all connected clients
- **What's missing:** No per-client filtering (by objective, event type, etc.)
- **Deferred to:** When a real dashboard/UI is built
- **Reason:** Clients can filter client-side. No production UI exists yet to motivate server-side filtering.

---

## Cross-Cutting Future Work

### Feature resurrection benchmark for context-engineering evaluation
- **Original scope:** New evaluation work driven by the harness-first / context-engineering direction
- **What exists:** Current public docs already position Tack around repo-aware execution, recovery, and upcoming discovery/context-dossier work. A draft benchmark note lives at `docs/plans/2026-04-09-feature-resurrection-benchmark-design.md`.
- **What's missing:** A canonical benchmark case, scoring rubric, and a repeatable run flow that exercises discovery, planning, execution, review, and merge end to end.
- **Deferred to:** After the next round of core context-gathering work starts landing, with a thin manual benchmark likely before full automation.
- **Reason:** Tack's current context flow is not strong enough yet for a fair automated benchmark, but the idea is important enough to capture now because it should shape future discovery and dossier design.
