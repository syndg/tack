# Deferred Items

Items intentionally deferred from their original phase. Each entry tracks where it was originally scoped, why it was deferred, and when it should land.

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
- **Details:** Implementation uses one `deck.db`. Design doc describes logical DBs (`objectives.db`, `agents.db`, etc.)
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
- **Reason:** Phase 3 now records the intent correctly, but there is still no runtime planner worker to act on it.
