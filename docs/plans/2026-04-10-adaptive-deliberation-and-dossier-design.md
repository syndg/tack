# Tack: Adaptive Deliberation And Context Dossier Design

**Date:** 2026-04-10  
**Status:** Draft direction / working design  
**Preserves:** `docs/plans/2026-04-02-context-engineering-and-harness-hardening-design.md`  
**Related:** `docs/plans/2026-04-02-blueprint-model-design.md`, `docs/plans/2026-04-09-retry-recovery-design.md`

## Why this exists

Tack's next major improvement is not more raw execution machinery. It is better shaping of human judgment, context, planning, and execution discipline.

The product should help teams do agentic coding at scale without turning humans into passive prompt submitters. Humans should continue to own intent, tradeoffs, ambiguity resolution, and architecture judgment. Tack should own the in-between machinery that makes execution more reliable.

This document defines the recommended product shape for:
- adaptive deliberation
- first-pass context and full dossiers
- interactive planning for major work
- blueprint and mode responsibilities
- a split execution architecture with a typed external tool plane

## Product shape

Tack should be built around one core promise:

> Humans keep judgment. Tack makes execution reliable.

Tack is not an autopilot coding agent. It is a workflow system that adapts its level of deliberation to the task while keeping the user-facing experience simple.

The expected user experience should remain:
- describe the task
- optionally choose `mode`
- optionally choose `blueprint`
- answer clarifying questions only when needed
- approve only when risk or workflow requires it
- let Tack handle the rest

Internally, Tack may do much more:
- build first-pass context automatically
- route mode
- deepen into a full dossier when justified
- generate candidate plans
- derive execution contracts
- run workers inside deterministic gates and recovery loops

This yields four internal layers:
- intake layer
- deliberation layer
- workflow layer
- execution layer

## Run lifecycle

Every run should start with a mandatory, internal **first-pass context** step. This step is not a user decision and should stay cheap enough to run on every request.

The default lifecycle should be:

1. request intake
2. first-pass context generation
3. mode routing
4. blueprint resolution
5. workflow execution
6. execution contract generation
7. workers, gates, recovery, completion

First-pass context must happen before blueprint execution so mode routing is not blind.

After first-pass context:
- Tack auto-selects the effective `mode`
- blueprint remains explicit if user-provided
- otherwise current default blueprint resolution continues
- Tack may return a non-blocking `suggested_blueprint` plus reason

Important internal rule:
- first-pass context is always automatic and internal
- full dossier generation is blueprint-controlled and threshold-driven

## Modes and blueprints

Tack should separate **mode** from **blueprint**.

A **mode** is Tack's internal deliberation posture:
- `direct`
- `guided`
- `planning`
- `debug`

A **blueprint** is the user or team workflow contract:
- what steps run
- where human approval appears
- whether review, security, or migration checks run
- how work fans out
- which deterministic actions run in sequence

Mode should auto-route by default. Blueprint selection should stay user-facing and effectively manual:
- if user specifies one, use it
- otherwise keep current default resolution behavior
- optionally return `suggested_blueprint` and `blueprint_reason`
- do not silently switch workflows

This keeps workflow policy under human and org control while still letting Tack be smart internally.

## Built-in blueprint templates

Ship a small built-in set for v1:

- `standard`
- `major-feature-interactive`
- `hotfix-debug`

### `standard`

The default balanced workflow.

- always uses first-pass context
- deepens to a full dossier only when threshold signals justify it
- requires human approval only when mode is `planning` or risk is high

### `major-feature-interactive`

The collaboration-heavy workflow for large feature work.

- builds a full dossier
- generates 3 candidate plans
- drives human back-and-forth until one plan is selected
- then moves into execution contract generation and worker execution

### `hotfix-debug`

The fast fix workflow.

- goes directly through repro, root-cause analysis, and fix by default
- avoids an explicit plan step unless ambiguity or risk rises enough to justify escalation

## Context model

The context system should have two layers: **first-pass context** and **full dossier**.

### First-pass context

First-pass context is:
- mandatory
- internal
- cheap
- operational rather than comprehensive

Its job is to support routing, question quality, and workflow shaping. It should answer things like:
- what parts of the repo are probably relevant?
- is this localized or cross-cutting?
- are tests, docs, or rules nearby?
- does this look like a bug fix, a major feature, or something ambiguous?

It should not try to fully solve the task.

### Full dossier

The full dossier is a planner-facing, objective-local research artifact used only when deeper deliberation is needed.

Its job is to provide a compact, cited understanding of:
- relevant files, modules, and symbols
- similar implementations or precedents
- applicable docs, rules, and plans
- constraints, risks, and unknowns
- suggested boundaries for decomposition

The dossier is not:
- long-term memory
- a giant prompt dump
- raw retrieval transcripts
- the final worker packet

It is the thing that lets Tack move from vague intent to planning-quality context.

## Dossier generation

The dossier should be produced by a dedicated discovery workflow, not by one free-form planner prompt.

The generation flow should be:

1. normalize the request into an objective summary and key terms
2. run deterministic-heavy retrieval to gather candidate evidence
3. collect compact evidence records with citations
4. use an agent to synthesize that evidence into a structured dossier
5. hand the dossier to planning

The retrieval layer should optimize for **recall**, not precision. Its job is not to perfectly identify the final relevant file set. Its job is to assemble a strong, inspectable candidate set cheaply.

The synthesis agent then narrows, compresses, and explains:
- what matters
- what patterns already exist
- what risks and unknowns remain
- what boundaries look cleanest
- what still needs human clarification

This hybrid model is the right default for large codebases:
- purely deterministic dossier generation is too brittle
- purely agentic wandering is too expensive, noisy, and hard to audit

So v1 should use deterministic candidate generation plus agent synthesis with citations.

## Human collaboration model

Tack should be designed so humans do not outsource judgment.

The collaboration model should be adaptive:
- small or local work: execute directly with minimal ceremony
- moderate work: ask a few focused questions if needed, then execute
- major feature work: run an interactive planning loop
- bug fixes: default to reproduce, diagnose, and fix directly unless ambiguity or scope forces escalation

For major feature work, Tack should support a planning conversation similar to a brainstorming loop:
- build a full dossier
- produce 3 candidate plans
- explain tradeoffs
- ask focused follow-up questions
- revise based on answers
- converge on one execution-ready plan

The human continues to own:
- intent
- tradeoffs
- ambiguity resolution
- architecture judgment
- product judgment
- final approval when risk is meaningful

Tack owns:
- evidence gathering
- option generation
- clarifying question quality
- execution discipline
- deterministic validation
- retries and recovery
- auditability and legibility

## Execution architecture

Tack should treat execution as three coordinated planes:

- **control plane**: Go daemon, orchestration, policy, approvals, audit, retries, recovery
- **tool plane**: typed external-tool discovery and invocation, likely through an `executor`-style TypeScript subsystem
- **repo plane**: repo-local file, shell, git, and test execution, ideally through isolated sandboxing and later stronger virtualization

The control plane remains the system of record.

The tool plane exists because model-authored typed tool use is better served by a TypeScript runtime. This is where MCP, OpenAPI, GraphQL, and internal APIs can be normalized into a common searchable catalog.

Workers should not receive giant tool dumps. They should get a small discovery surface like:
- `search`
- `describe`
- `invoke`

The repo plane stays separate because code mutation and validation have different requirements from external-tool invocation.

## Execution contract

The key handoff artifact is the **execution contract**.

Tack should derive an execution contract from the selected plan and dossier. Each worker should get:
- precise scope
- selected repo context
- allowed tool namespaces
- acceptance criteria
- risks and watchouts
- approval and policy constraints

This keeps context bounded all the way through the system:
- dossier for planning
- execution contract for worker execution
- gates and recovery for reliability

Tack may integrate an `executor`-like subsystem as the typed tool plane, but Tack should continue to own workflow semantics.

## Configurability and control

Tack should be opinionated in product behavior but configurable at the workflow boundary.

The split should be:

### Code-owned product semantics

- first-pass context always runs
- mode routing exists
- dossier is structured and cited
- recovery stays coordinator-owned
- execution remains bounded and auditable

### Blueprint-owned workflow

- whether a full dossier step runs
- whether planning is interactive
- how many candidate plans to generate
- where approval happens
- which deterministic actions run
- whether review, security, or migration checks are included

### Config, policy, and plugin-owned extensions

- custom context sources
- custom retrieval providers
- custom deterministic actions
- org safety rules
- path-sensitive approval requirements
- external tool catalogs

This keeps blueprints valuable without turning them into a DSL for retrieval ranking, recovery logic, or policy semantics.

## UX contract

The UX contract should be simple enough that users do not need to understand Tack's internal architecture to get value.

The default interaction should feel like:
- describe the task
- optionally choose a mode
- optionally choose a blueprint
- answer clarifying questions only if needed
- approve only when risk or workflow requires it
- let Tack handle the rest

Internal sophistication should stay internal.

The governing UX principle should be:

> Internal sophistication, external simplicity.

Whenever a new subsystem is introduced, Tack should ask whether it improves outcomes without forcing users to learn more machinery.

## Recommendation summary

The recommended design is:
- always run an internal first-pass context step before mode routing
- auto-route mode, but keep blueprint selection manual and default-driven
- optionally suggest a better blueprint, non-blocking
- keep full dossier generation as a built-in blueprint action
- use a hybrid dossier pipeline: deterministic candidate retrieval plus agent synthesis
- reserve interactive planning for major feature work and other high-ambiguity or high-risk cases
- keep bug fixes diagnosis-first and low-ceremony by default
- separate control plane, tool plane, and repo plane
- let Tack own workflow semantics while integrating an `executor`-style typed tool layer as a pluggable subsystem
- keep blueprints focused on workflow orchestration, not internal heuristic programming
- treat UX simplicity as a hard product constraint

The resulting product identity is:

> Tack is a harness engineering system for human-led, agent-assisted software execution at scale.
