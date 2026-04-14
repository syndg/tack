# Deferred Items

Items intentionally deferred from their original phase or parked for later design. Each entry tracks where it was originally scoped, why it was deferred, and when it should land.

For current product behavior, start with `docs-site/content/docs/` and `docs/README.md`. This file is the future-work backlog, not the current-state source of truth.

---

## From Phase 1 (Foundation)

### Daytona sandbox provider hardening
- **Original scope:** Design doc Phase 1 — "Sandbox provider interface + Daytona implementation"
- **What exists:** Concrete Daytona provider code now lives under `internal/sandbox/daytona/` and is part of the current sandbox matrix.
- **What's missing:** Broader production hardening, integration coverage, and clearer operational defaults now that both local and Daytona execution paths exist.
- **Deferred to:** Future execution hardening work
- **Reason:** The provider exists, so the remaining work is no longer basic implementation; it is operational hardening.

### Pi runtime hardening
- **Original scope:** Design doc Phase 1 — "Agent runtime interface + Pi implementation"
- **What exists:** Concrete Pi runtime code now lives under `internal/runtime/pi/` and is used by current benchmark and execution flows.
- **What's missing:** Further runtime hardening, default alignment, and any remaining ergonomics work now that Pi is a real execution path rather than an interface placeholder.
- **Deferred to:** Future runtime hardening work
- **Reason:** The implementation exists. Remaining work is quality-of-life and operational maturity, not initial bring-up.

### Config defaults drift from design doc
- **Details:** Several `internal/config/config.go` defaults diverge from design doc:
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

### Benchmark/report productization after feature-resurrection rollout
- **Original scope:** New evaluation work driven by the harness-first / context-engineering direction
- **What exists:** Built-in benchmark specs, hardness freezing, end-to-end benchmark reports, and completed reruns for the lazygit command-log and undo resurrection cases. The benchmark design notes live under `docs/plans/2026-04-09-feature-resurrection-benchmark-design.md` and `docs/plans/2026-04-11-lazygit-benchmark-v1-design.md`.
- **What's missing:** Richer report surfaces, more benchmark families, structured validation modes beyond raw shell strings, and stronger operator-facing summaries of dossiers, stream cards, and insights.
- **Deferred to:** Follow-on benchmark productization work after the core context-engineering rollout
- **Reason:** The benchmark foundation now exists. The remaining work is expanding coverage and improving reporting, not establishing the first benchmark flow.
