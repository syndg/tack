# Context Engineering & Harness Hardening Plan

**Related design:** `docs/plans/2026-04-02-context-engineering-and-harness-hardening-design.md`

This plan preserves the original orchestrator roadmap and inserts a focused phase of work before TUI / GUI / web / channel-heavy work.

---

## Outcome

Before prioritizing new user surfaces, Tack should become significantly better at:
- objective-local research
- context selection
- task shaping
- worker packet quality
- repository legibility
- codifying repeated learnings into the harness

---

## Phase 5.5 — Context Engineering & Harness Hardening

### 1. Discovery workflow MVP
- [ ] Add `internal/services/discovery/` as a first-class workflow boundary before planning
- [ ] Define minimal discovery inputs / outputs
- [ ] Support deterministic retrieval for objective-local research
- [ ] Support compact, cited findings output
- [ ] Keep v1 objective-local; defer global memory

### 2. Context dossier MVP
- [ ] Define the `Context Dossier` structure
- [ ] Decide whether v1 is persisted or transient
- [ ] Capture relevant files, patterns, risks, unknowns, and citations
- [ ] Make dossier the input to planning rather than raw objective text alone

### 3. Planner integration
- [ ] Update planning flow to consume dossier outputs
- [ ] Separate discovery concerns from decomposition concerns
- [ ] Keep the planner focused on slicing / boundary setting / stream generation

### 4. Execution contract generation
- [ ] Define worker-facing execution contracts
- [ ] Attach dossier-derived context to each stream
- [ ] Include scope, acceptance criteria, risks, and deterministic checks
- [ ] Improve worker packet quality without bloating worker context

### 5. Repository legibility baseline
- [ ] Define a recommended in-repo knowledge structure for Tack projects
- [ ] Define path-scoped rule best practices
- [ ] Define architecture map / design map expectations
- [ ] Identify which checks should be enforced mechanically vs documented only

### 6. Codification loop
- [ ] Define how repeated human corrections become docs / rules / lints / blueprint defaults
- [ ] Add a simple path for promoting objective-local learnings into durable artifacts
- [ ] Keep persistent memory deferred until objective-local discovery is strong

### 7. Future memory layer (not immediate)
- [ ] Define how dossiers can later be distilled into project memory
- [ ] Define what should become persistent vs remain objective-local
- [ ] Avoid building a giant undifferentiated memory system too early

---

## Concrete implementation sequence against the current codebase

This sequence is intentionally incremental. It avoids a broad subsystem rewrite and instead deepens the new context layer around the existing run-centric and planning workflow structure.

### Step 1: Introduce discovery as a new workflow seam

**Goal:** add a small orchestration boundary that owns pre-planning research.

**Likely files/packages:**
- new: `internal/services/discovery/service.go`
- new: `internal/services/discovery/types.go`
- possibly new tests: `internal/services/discovery/service_test.go`
- wiring: `internal/daemon/daemon.go`

**Responsibilities:**
- accept objective ID + objective text
- run deterministic retrieval first
- return a structured `ContextDossier`
- remain independent of planning persistence at first

**Do not do yet:**
- broad persistent memory
- channel/TUI integration
- broad new DB schema unless clearly needed

### Step 2: Build deterministic retrieval primitives first

**Goal:** make discovery useful before involving more agent behavior.

**Likely implementation areas:**
- new package or subpackage: `internal/services/discovery/retrieval.go`
- likely use shell-backed repo inspection first rather than speculative indexing infrastructure
- may consult existing docs/rules paths from project config and repo layout

**Expected capabilities for v1:**
- locate likely relevant files/modules
- find similar implementations
- surface relevant rules/docs files
- capture cited evidence

**Design constraint:**
- results should be compact and ranked
- raw noisy command output should not become the main artifact

### Step 3: Define the Context Dossier artifact

**Goal:** create the first-class thing that planning consumes.

**Likely implementation areas:**
- `internal/services/discovery/types.go`
- possibly later `internal/domain/types.go` if persistence becomes necessary
- maybe API response types in daemon/client later, but not required for v1

**Suggested v1 fields:**
- objective summary
- relevant files/modules
- similar patterns / precedents
- applicable docs/rules
- risks / unknowns
- suggested boundaries
- citations

**Key rule:**
- dossier is planner-facing first, UI-facing second

### Step 4: Integrate discovery into the planning workflow

**Goal:** make planning consume researched context rather than only objective text.

**Likely implementation areas:**
- `internal/services/planner/workflow.go`
- `internal/services/planner/planner.go`
- maybe `internal/services/dispatch/execution_runner.go` for planner-created plans
- thin route changes only if required

**Approach:**
- keep `decompose.go` as parser/validator/compiler
- avoid bloating daemon routes
- likely add a planning entry point that accepts dossier + planner output flow

### Step 5: Add execution contract generation

**Goal:** improve the packet each worker receives.

**Likely implementation areas:**
- new: `internal/services/agents/contracts.go` or `internal/services/discovery/contracts.go`
- `internal/services/agents/overlay.go`
- possibly `internal/services/dispatch/spawner.go`

**Expected change:**
- overlays/contracts should include:
  - precise scope
  - selected context from dossier
  - acceptance criteria
  - risks/watchouts
  - deterministic checks
- avoid dumping the entire dossier into every worker packet

### Step 6: Add minimal persistence only if the workflow needs it

**Goal:** resist premature schema expansion.

**Options:**
- keep dossier transient in v1 if only planner/executor need it immediately
- add persistence later only if needed for:
  - approvals/editing
  - auditability
  - TUI/web visualization
  - future distillation into memory

**If persistence is needed:**
- prefer a narrow objective-scoped table/JSON blob over a sprawling knowledge schema

### Step 7: Add repo-legibility guidance and checks

**Goal:** make Tack better at helping repos become understandable to agents.

**Likely areas:**
- docs and templates first
- later config support and linting
- possible future package: `internal/services/knowledge/` or repo checks under harness/config

**Deliverables:**
- recommended docs structure
- path-scoped rule guidance
- architecture/design map expectations
- simple checks for freshness / presence later

### Step 8: Add codification hooks after the dossier is real

**Goal:** let repeated learnings become durable harness elements.

**Possible later shapes:**
- promote findings into docs/rules
- generate follow-up codification tasks
- suggest blueprint/rule updates
- add recurring cleanup/background tasks later

**Important:**
- do this only after the discovery+dossier flow proves useful

---

## Suggested implementation order for actual coding work

1. `internal/services/discovery/` package skeleton + tests
2. deterministic retrieval + dossier generation
3. planner workflow integration
4. worker execution contract / overlay enrichment
5. only then evaluate persistence requirements
6. then add repo-legibility helpers / codification pathways

This keeps the work aligned with the current codebase shape:
- `runs` remains the execution boundary
- `planner` remains the planning workflow boundary
- `discovery` becomes the new pre-planning workflow boundary
- daemon routes stay thin
- future TUI/web/channel work can consume these deeper artifacts later

---

## After Phase 5.5

Resume the original roadmap with stronger foundations for:
- TUI / GUI / web control surfaces
- Discord / Slack / channel integrations
- autonomy and health features
- richer observability and operator workflows

These surfaces should expose and control the stronger context/harness layer rather than compensating for its absence.
