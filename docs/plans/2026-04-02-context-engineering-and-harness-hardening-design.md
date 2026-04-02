# Tack: Context Engineering & Harness Hardening

**Date:** 2026-04-02  
**Status:** Draft direction / working design  
**Supersedes:** Nothing  
**Preserves:** `docs/plans/2026-02-25-orchestrator-design.md`

## Why this exists

Tack's original orchestrator design remains correct at the platform level: daemon-first orchestration, deterministic blueprints, planning, execution, merge, and later interaction surfaces like TUI and channels.

However, after implementing the first five phases of Tack's core machinery, it is clear that the current bottleneck is not primarily more execution machinery or more user surfaces. The real leverage is in **context engineering** and **harness engineering**.

The key shift is:

> Tack should not primarily compete on being an agent runner.  
> Tack should compete on being the best programmable harness for software work.

This means Tack should help developers turn a vague objective into a **well-researched, well-bounded, context-rich execution packet**, and then run workers inside deterministic control loops.

The original plan stays intact. This document defines the **new next phase of work** that should happen before TUI / GUI / web / Slack / Discord become the main focus.

---

## Core philosophy

### The model is not the product

The product is the harness:
- the context an agent gets
- the tools it can use
- the rules that apply
- the deterministic checks that constrain it
- the structure of the workflow
- the way failures are captured and engineered away

Agents are workers. Tack is the system that makes workers reliable.

### Harness engineering is the center of gravity

Tack already has a strong orchestration substrate:
- daemon
- runs boundary
- blueprint engine
- planning / approval flow
- execution coordination
- merge queue
- deterministic quality gates

That machinery is necessary, but it is not the deepest moat.

The moat is:
- giving agents the **right context**
- keeping context windows **small and high-signal**
- slicing work into **bounded packets**
- encoding codebase conventions and human taste into the harness
- preserving legibility so future runs get smarter

### Objective-local context comes first

Tack should eventually grow a persistent project memory layer, but that should not be the first investment.

The first priority is **objective-local context engineering**:
- for this task, what code matters?
- what conventions apply here?
- what similar implementations already exist?
- what are the likely dependencies, risks, and unknowns?
- what is the cleanest way to bound the work?

Persistent memory should come later, as a distillation layer built on top of successful objective-local discovery.

### Planning is not the first intelligent step anymore

The current shape of the system still puts too much burden on planning.

The future flow should be:
1. objective
2. discovery / research
3. context dossier
4. planning / decomposition
5. execution contracts
6. workers
7. review / merge
8. learning / codification

Planning should consume **structured research outputs**, not raw objective text plus an arbitrarily large codebase.

---

## The new north star

Tack should become:

> An opinionated but programmable harness engineering framework for codebases.

Opinionated about principles:
- progressive disclosure
- bounded context
- deterministic verification
- repo legibility
- encoded invariants
- isolated execution
- human attention as the scarce resource

Programmable in implementation:
- custom blueprints
- custom rules
- custom tool catalogs
- custom context gathering steps
- custom verification / back-pressure
- custom approval and autonomy policies

## Product thesis

### What Tack is

Tack is a harness engineering system for software work.

Its job is to help a team turn:
- vague objectives
- large and partially illegible codebases
- non-deterministic model behavior

into:
- context-rich, bounded work packets
- deterministic control loops
- reliable execution at scale

### What Tack is not

Tack is not primarily:
- a chat app for coding agents
- a TUI product with orchestration attached
- a pile of MCP integrations
- a generic multi-agent roleplay framework
- a model company

The differentiator is not the worker. The differentiator is the harness around the worker.

### Core product promise

A team should be able to use Tack's primitives to shape agent behavior around its own codebase realities:
- its architecture
- its docs quality
- its conventions
- its operational safeguards
- its preferred balance of autonomy and control

The product promise is:

> Give developers the primitives to make their repository legible to agents, shape context precisely, and run personalized agentic workflows safely.

### Strategic implication

This means Tack should prioritize:
1. context quality before UI breadth
2. execution packet quality before worker proliferation
3. codification before ad-hoc prompting
4. legibility before autonomy expansion
5. operator attention efficiency before feature surface area

---

## Design principles

### 1. Progressive disclosure over context stuffing

Do not dump the whole world into the agent.

Prefer:
- short always-on instructions
- path-scoped rules
- task-specific retrieval
- compact research artifacts
- explicit citations to source material
- loading deeper docs only when needed

Reject:
- giant monolithic `AGENTS.md`
- giant default prompts
- overly broad tool sets
- raw noisy exploration transcripts in parent threads

### 2. Sub-agents are context firewalls

Sub-agents should exist primarily for context control, not roleplay.

They should be used for:
- locating definitions
- tracing flows
- finding similar implementations
- identifying risks
- analyzing code patterns
- gathering evidence

Parent/orchestrator contexts should receive condensed, cited outputs rather than raw intermediate noise.

### 3. Deterministic retrieval backbone + agent synthesis

Tack should not rely only on free-form agent exploration.

Prefer a hybrid:
- deterministic retrieval gathers candidate evidence
- focused sub-agents synthesize and compress findings
- planner consumes structured dossier outputs
- workers receive bounded execution contracts

### 4. Silent success, loud failure

Deterministic checks should be cheap, local, and quiet on success.
Only failures should expand into context.

This applies to:
- quality gates
- structural validations
- lints
- doc freshness checks
- repo integrity checks
- execution retries

### 5. Encode repeated human corrections into the harness

When humans repeatedly correct the same class of issue, Tack should help teams turn that correction into:
- documentation
- scoped rules
- custom lints
- blueprint steps
- review heuristics
- verification gates
- reusable skills

### 6. Repository legibility is a product feature

A codebase that agents cannot navigate is effectively opaque.

Tack should help teams make repositories more legible by encouraging:
- stable docs structure
- architecture maps
- execution plans in-repo
- path-scoped rules
- generated references
- structural constraints
- recurring cleanup / codification loops

---

## What stays the same from the original plan

The original orchestrator design remains valid:
- daemon-first architecture
- TUI as client, not product core
- deterministic blueprints mixing agentic and non-agentic steps
- runs / plans / streams / merge queue lifecycle
- isolated sandboxes / workspaces
- human approval and intervention surfaces
- future TUI, web, and messaging surfaces

This document does **not** replace that architecture.
It changes **what Tack should focus on next**.

---

## What changes in the roadmap

The original roadmap has:
- Phase 1: Foundation
- Phase 2: Harness Core
- Phase 3: Planning
- Phase 4: Execution
- Phase 5: Merge & Review
- Phase 6: TUI Client
- Phase 7: Autonomy & Health
- Phase 8: Polish & Ecosystem

### Revised sequencing

Phases 1-5 remain intact as the machinery phase.

Before moving heavily into Phase 6 surfaces, Tack should add a new focus area:

## Phase 5.5: Context Engineering & Harness Hardening

This is not a repudiation of the original roadmap. It is a refinement based on what has been learned from implementing the machinery and from industry research.

After this phase, Tack can continue with:
- TUI / GUI / web observability and control surfaces
- channels (Discord / Slack)
- richer autonomy controls
- broader ecosystem support

Those surfaces will be more valuable once the context layer is strong.

---

## Phase 5.5 goals

### Goal A: Add a discovery workflow before planning

Introduce a first-class discovery / research workflow that runs before or alongside planning.

Its job is to answer:
- what modules / files / symbols are relevant?
- what existing patterns are similar?
- what codebase conventions apply?
- what dependencies / edge cases are likely?
- what are the key risks and unknowns?
- what should be clarified before workers start?

### Goal B: Introduce a first-class Context Dossier artifact

For each objective, Tack should be able to produce a structured artifact that captures objective-local research.

Suggested contents:
- objective summary
- impacted files / modules
- relevant symbols / interfaces
- similar implementations / precedents
- applicable rules / conventions
- dependencies and risks
- suggested stream boundaries
- open questions / unknowns
- citations to source files/docs/commands

This dossier becomes the main input to planning.

### Goal C: Improve worker packet quality via Execution Contracts

Workers should not receive only a broad plan stream description.
They should receive a bounded execution contract containing:
- exact scope
- relevant context excerpts
- acceptance criteria
- interfaces touched
- risks / watchouts
- deterministic checks required
- escalation conditions

### Goal D: Strengthen repo-legibility primitives

Tack should offer stronger support for codebase legibility and codification, such as:
- recommended docs structure
- architecture maps
- path-scoped rules
- decision logs / execution plans in-repo
- generated references
- doc linting / freshness checks
- structural invariants

### Goal E: Establish a path toward persistent memory

Do not build a giant memory layer immediately.
Instead, define how successful objective-local discovery outputs can later be distilled into:
- project conventions
- recurring patterns
- architecture notes
- reusable skills / rule modules
- quality heuristics

---

## Proposed architecture additions

### 1. Discovery workflow boundary

Add a new workflow boundary ahead of planning.

Preferred package shape:
- `internal/services/discovery/`

Rationale:
- keeps the concern explicit and objective-local
- avoids overloading `planner` with pre-planning research duties
- leaves room for a later `context` layer that may include both discovery and persistent memory

Responsibilities:
- coordinate task-local research
- run deterministic retrieval steps
- optionally dispatch focused sub-agents
- aggregate findings into a structured dossier
- expose thin orchestration methods to daemon / CLI / future UI

This should be a workflow/orchestration boundary, similar in spirit to the recent planning workflow deepening.

### 2. Context dossier domain model

Introduce a first-class domain artifact for discovery output.

Likely v1 shape:
- objective-scoped
- persisted or at least serializable
- designed for planner consumption first, UI consumption second

Possible persisted entities later:
- `ContextDossier`
- `ContextFinding`
- `ObjectiveEvidence`
- `KnowledgeArtifact`

This does not need to be overbuilt immediately. The first version can be simple and objective-scoped.

### 3. Retrieval and synthesis split

Discovery should separate:
- deterministic retrieval
- synthesized interpretation

Deterministic retrieval may include:
- code search
- symbol lookup
- blueprint/rule lookup
- similar file detection
- docs lookup
- repo structure traversal
- diff / blame / history summaries

Synthesis may include:
- compressing findings
- ranking relevance
- identifying risks
- proposing boundaries
- highlighting unknowns

### 4. Execution contract generation

Add a layer that transforms plan streams + dossier findings into worker-ready contracts.

This is where Tack's value compounds:
- planners produce the decomposition
- context layer enriches each slice
- workers receive high-signal packets

### 5. Codification loop

Over time, create an explicit path for promoting repeated learnings into durable harness elements:
- docs
- rules
- skills
- validations
- blueprint defaults

## Key design bets

### Bet 1: Discovery should be objective-local first

Tack should first become excellent at answering: "what matters for this task right now?"

Not: "what is everything we have ever learned about this repo?"

### Bet 2: The first artifact should be a Context Dossier

Before building a broad project memory system, Tack should produce a per-objective dossier that:
- is compact
- is cited
- is actionable
- feeds directly into planning and execution

### Bet 3: Repo knowledge should be legible, not magical

Tack should encourage teams to store important knowledge in-repo as versioned artifacts:
- architecture maps
- product/design docs
- scoped rules
- execution plans
- generated references

This is better than relying on hidden external memory for core behavior.

### Bet 4: Workers are replaceable; execution packets are not

The system should treat builder/reviewer/merger agents as workers whose quality depends heavily on the packet they receive.

The packet quality is a more important product surface than multiplying worker roles.

### Bet 5: TUI/channels should expose the stronger system later

TUI, GUI, web, Slack, and Discord are still important.
But they should sit on top of a stronger context/harness substrate rather than being the next attempt to compensate for missing intelligence.

---

## Product primitives Tack should emphasize

### Context primitives
- path-scoped rules
- codebase map generation
- dossier generation
- pattern extraction
- source citation
- objective-local retrieval
- later: persistent memory distillation

### Workflow primitives
- blueprints
- deterministic nodes
- agent nodes
- retries / fix loops
- approvals / escalations
- merge policies
- bounded CI loops

### Legibility primitives
- docs structure
- architecture constraints
- execution plans
- generated references
- doc linting
- structural tests
- quality / debt tracking

### Execution primitives
- isolated sandboxes / workspaces
- curated tool access
- runtime abstraction
- quality gates
- local verification loops
- worker execution contracts

### Interaction primitives
- CLI first
- APIs throughout
- TUI / web later
- channels later

---

## Non-goals for this next phase

The next phase should **not** prioritize:
- building the TUI first
- building Slack / Discord first
- adding many new user surfaces before the context layer improves
- creating a giant always-on memory blob
- adding large undifferentiated MCP catalogs
- creating many persona-style agents without clear context-isolation purpose

These may still happen later, but they should not displace the context engineering work.

---

## Concrete next-step themes

### Theme 1: Objective-local discovery MVP
Create a narrow first version that can answer:
- what matters for this objective?
- what evidence supports that?
- what context should the planner and workers actually see?

### Theme 2: Context dossier MVP
Define the dossier format and integrate it into the planning flow.

### Theme 3: Repo-legibility baseline
Define what Tack expects or encourages in-repo:
- docs map
- architecture overview
- rule scoping
- decision / execution plan storage

### Theme 4: Codification pathways
Make it easy to promote learnings into:
- docs
- rules
- lints
- blueprint guidance

### Theme 5: Keep the UI roadmap intact, but later
Once the context/harness layer is meaningfully stronger, continue the original product roadmap with:
- TUI
- GUI / web surfaces
- channels
- richer observability and control surfaces

---

## Working thesis

Tack's first five phases built the machinery.
The next differentiating phase should build the intelligence wrapper around that machinery.

That wrapper is not "more AI" in the abstract.
It is:
- better context selection
- better context isolation
- better task shaping
- better deterministic feedback loops
- better codification of what the system learns

If Tack succeeds here, the TUI / GUI / channel surfaces will expose a much stronger system rather than compensating for a weak one.

---

## Short version

Tack is moving from:

> an orchestrator that can run agent workflows

Toward:

> a harness engineering framework that helps developers make their repositories legible, shape context precisely, and run agentic coding workflows safely and reliably at scale.

The original roadmap remains valid.
The next priority is to deepen the context engineering and harness hardening layer before investing heavily in new interaction surfaces.
