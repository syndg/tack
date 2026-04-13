# Parent PRD: Context Engineering Core

## Problem Statement

Tack's current orchestration machinery is good enough to run real work, but it still asks the wrong subsystem to do too much thinking at the wrong time. Planning still performs too much code exploration. Builders still have to infer architectural intent that should have been made explicit earlier. Reviewers still discover missing implementation constraints late and repeatedly, which creates avoidable churn, extra retries, and human rescues.

Recent benchmark runs made the failure mode concrete. Reliability fixes improved completion, but autonomy remained weak because the system still learned too much of the real contract during review. The planner, builder, and reviewer do not yet have a clean separation of responsibility. Repo exploration, decomposition, implementation, and contract validation are still too entangled.

From the user's perspective, the product problem is simple: Tack should turn a vague software objective into bounded, high-signal execution packets without making the planner wander the repo, without making the builder improvise architecture, and without making the reviewer act like the real planner.

This parent PRD defines the core context-engineering architecture that fixes that boundary. It introduces a persisted, editable dossier produced before planning, a planner that consumes dossier context instead of exploring the repo, first-class persisted stream cards as execution contracts, and typed contract failure semantics that distinguish builder mistakes from missing or contradictory contracts.

It also introduces the first layer of implicit knowledge capture. Tack should not wait for a giant future memory system to start learning. It should begin by recording objective-local signals generated during dossier editing, plan approval, review churn, retries, and human guidance. Those signals should become a durable objective-scoped artifact that can later improve execution contracts and eventually drive codification into reusable harness primitives.

## Solution

Tack will add three first-class workflow seams to its core execution architecture.

The first seam is discovery. Discovery will own repo exploration and objective-local research. It will use deterministic retrieval plus agent synthesis to produce a persisted, collaboratively editable dossier for each objective. The dossier will capture relevant code, similar patterns, risks, suggested seams, and cited architectural constraints.

The second seam is dossier-driven planning. Planning will stop exploring the codebase directly. Instead, it will consume the user objective, the dossier, and workflow constraints, then emit persisted issue-like stream cards. Stream cards will include scope, blocked-by relationships, acceptance criteria, proof expectations, hard implementation anchors, and citations for those anchors. Planner may override dossier-proposed seams, but must explain why.

The third seam is contract-driven execution. Builders will become intentionally dumb executors of stream cards. Reviewers will validate only against the stream card and dossier-backed constraints attached to that stream. If a card is contradictory or insufficient, builders will return `contract_blocked`. If reviewers discover a missing required constraint, they will return `contract_gap`, which will trigger localized replanning for the affected stream rather than broad review churn.

Alongside those seams, Tack will add an objective-local implicit knowledge layer. The first artifact in that layer will be an append-only `Objective Insight Log` that captures normalized learning signals during the lifecycle of a single objective. This log will not act as global memory. Instead, it will provide durable local intelligence that can be used to preserve unresolved findings, explain repeated corrections, and later compile objective-local contract patches. Only after this objective-local loop proves useful should Tack promote repeated patterns into cross-objective codification candidates such as rules, review heuristics, or lints.

## User Stories

1. As a developer, I want Tack to research the repo before planning, so that plans are based on evidence rather than prompt guessing.
2. As a developer, I want the planner to stop exploring code directly, so that planning quality can be measured separately from discovery quality.
3. As a developer, I want an objective-local dossier artifact, so that the context behind a plan is inspectable and durable.
4. As a developer, I want the dossier to be editable, so that I can correct or refine important context before execution diverges.
5. As a developer, I want dossier generation to proceed automatically by default, so that routine work does not require extra ceremony.
6. As a developer, I want planning to consume dossier context rather than raw objective text alone, so that decomposition reflects repo realities.
7. As a developer, I want the dossier to propose likely decomposition seams, so that planning starts from informed boundaries instead of inventing them blindly.
8. As a developer, I want the planner to be allowed to override dossier seams only with explicit rationale, so that decomposition decisions stay auditable.
9. As a developer, I want each stream to be represented as a first-class card, so that builders and reviewers operate on a precise contract instead of a prose blob.
10. As a developer, I want stream cards to define acceptance criteria, so that correctness is explicit before work starts.
11. As a developer, I want stream cards to define implementation scope, so that builders do not spread changes across unrelated areas.
12. As a developer, I want stream cards to define proof and test scope, so that verification work is planned rather than rediscovered in review.
13. As a developer, I want stream cards to include hard implementation anchors, so that codebase-specific architectural constraints are front-loaded to the builder.
14. As a developer, I want every hard implementation anchor to cite dossier evidence, so that architectural instructions are grounded in observed repo context.
15. As a developer, I want builders to follow the stream card strictly, so that execution is disciplined and bounded.
16. As a developer, I want builders to fail fast when the contract is insufficient or contradictory, so that Tack stops guessing and surfaces the real bottleneck.
17. As a developer, I want reviewers to validate against the stream card and dossier-backed constraints only, so that review remains a contract check rather than a second planning pass.
18. As a developer, I want missing constraints discovered during review to be classified as `contract_gap`, so that the system can fix the plan instead of blaming the builder for missing architecture.
19. As a developer, I want contradictory or unusable contracts discovered during implementation to be classified as `contract_blocked`, so that Tack can distinguish bad contracts from bad coding.
20. As a developer, I want `contract_gap` handling to replan only the affected stream, so that stable parts of the objective do not churn unnecessarily.
21. As a developer, I want planning to be able to request dossier expansion, so that weak discovery results can be deepened without breaking the clean boundary between discovery and planning.
22. As a developer, I want blueprint configuration to control whether one or multiple plans are generated, so that workflow policy stays explicit and team-controlled.
23. As a developer, I want the same core architecture to support benchmarks, so that autonomy and context quality can be measured on repeated hard tasks.
24. As a developer, I want repeated reviewer and human corrections to become easier to codify later, so that objective-local learnings can eventually become durable harness improvements.
25. As an operator, I want dossier, stream card, and failure semantics to be persisted, so that future UI and reporting surfaces can explain why a run succeeded, failed, or needed intervention.
26. As a developer, I want Tack to capture implicit learnings from dossier edits, plan approvals, reviewer findings, and human guidance, so that the system gets smarter within the current objective.
27. As a developer, I want those implicit learnings to stay objective-local at first, so that Tack does not overfit noisy run-specific facts into fake global memory.
28. As a developer, I want repeated review and retry signals to be preserved as a durable insight log, so that later retries and localized replanning use accumulated context instead of starting from scratch.
29. As a developer, I want objective-local insights to be compilable into contract patches, so that important constraints can be fed back into execution without a human manually restating them every time.
30. As an operator, I want repeated objective-local insights to become codification candidates later, so that stable repeated learnings can graduate into rules, heuristics, or checks after human review.

## Implementation Decisions

- Introduce discovery as a first-class workflow boundary that runs before planning.
- Treat the dossier as a first-class persisted objective-local artifact from day one.
- Support collaborative dossier editing in v1, while still auto-proceeding to planning by default unless a human intervenes.
- Keep discovery responsible for repo exploration using deterministic retrieval plus agent synthesis.
- Keep planner strictly forbidden from fresh repo exploration.
- Make planner consume objective input, dossier input, and workflow constraints only.
- Make the planner return `needs_dossier_expansion` when the dossier is insufficient for trustworthy decomposition.
- Allow discovery to suggest decomposition seams inside the dossier.
- Allow planner to override suggested seams, but require explicit rationale for doing so.
- Represent plan output as persisted issue-like stream cards rather than overloaded stream descriptions.
- Include, at minimum, title, goal, blocked-by graph, acceptance criteria, implementation scope, proof scope, hard implementation anchors, and citation-backed rationale in each stream card.
- Treat implementation anchors as hard constraints by default, not hints.
- Require every hard implementation anchor to cite dossier evidence.
- Keep builders intentionally narrow: implement the stream card exactly, stay in scope, and return `contract_blocked` rather than improvising.
- Keep reviewers intentionally narrow: validate only against the stream card and dossier-backed constraints attached to that stream.
- Introduce typed failure outcomes for at least `contract_blocked` and `contract_gap`.
- Route `contract_gap` back into localized replanning of the affected stream card only.
- Keep broader objective structure stable unless dependency logic requires further changes.
- Treat plan multiplicity as workflow or blueprint configurable rather than hardcoded.
- Model three core new workflow seams as first-class modules: discovery, dossier-driven planning, and execution-contract / stream-card consumption.
- Introduce an objective-local implicit knowledge layer rather than jumping immediately to global memory.
- Make the first implicit knowledge artifact an append-only persisted `Objective Insight Log`.
- Capture, at minimum, signals from dossier edits, plan approvals or edits, reviewer rejections, retries with human guidance, and other repeated objective-local corrections.
- Keep explicit repo rules and blueprint policy as first-class repo priors, separate from learned implicit knowledge.
- Treat the insight log as the source for future derived artifacts rather than a free-form prompt dump.
- Add `Contract Patches` as a derived layer that can later compile objective-local insights into planner- and execution-facing constraints.
- Add `Codification Candidates` as a later derived layer that proposes promotion of repeated insights into durable harness artifacts after human review.
- Keep implicit knowledge objective-local first; defer broad cross-objective memory and automatic global promotion.
- Favor deep modules that encapsulate stable behavior behind simple interfaces, especially for dossier generation, stream-card compilation, and contract failure handling.

## Testing Decisions

- Good tests should verify observable workflow behavior and artifact semantics, not prompt phrasing or incidental internal implementation details.
- Discovery tests should verify that deterministic retrieval and synthesis produce dossier outputs with the expected sections, citations, and seam suggestions for representative objectives.
- Dossier persistence tests should verify creation, retrieval, editing, and workflow handoff behavior.
- Planner tests should verify that planning consumes dossier input only, emits valid stream cards, preserves citations on hard anchors, and returns `needs_dossier_expansion` when context is insufficient.
- Contract compilation tests should verify that builder- and reviewer-facing execution packets are derived correctly from stream cards and dossier-backed constraints.
- Failure semantics tests should verify that `contract_blocked` and `contract_gap` take the correct routing path and do not collapse into generic review rejection behavior.
- Localized replanning tests should verify that `contract_gap` updates only the affected stream card and preserves unaffected plan structure.
- Objective insight tests should verify that signals from review, retry, approval, and dossier editing are persisted in normalized objective-local form.
- Derived-artifact tests should verify that future contract patches are compiled from objective-local insights rather than ad-hoc string concatenation or prompt stuffing.
- Codification-candidate tests should verify that repeated normalized signals can be promoted into reviewable candidates without immediately mutating repo-wide rules.
- Integration tests should cover end-to-end flow: objective -> dossier -> planning -> stream cards -> builder/reviewer execution -> typed contract failure handling.
- Benchmark-oriented tests and fixtures should be used as prior art, especially around review churn, human intervention counts, and stream-localized retry behavior.
- Where possible, tests should use narrow deterministic fixtures and assert on artifact shape, status transitions, and typed failure outcomes.

## Out of Scope

- Long-term project memory or cross-objective knowledge distillation.
- Full UI, TUI, or web editing surfaces for dossiers and stream cards.
- Broad codification automation that promotes objective-local learnings into permanent rules, docs, or lints without human review.
- Final charting, graphing, or benchmark dashboard visualization work.
- Full redesign of all existing workflows outside the context-engineering core.
- Rich multi-user collaboration semantics beyond basic persisted artifact editing.
- Replacing all existing retry and recovery semantics in one step beyond the typed contract failure paths introduced here.
- Global always-on memory that automatically influences unrelated future objectives.

## Further Notes

- This parent PRD is intended to anchor multiple smaller slice PRDs. The implementation should be broken into separate independently shippable slices rather than landed as one monolith.
- Recommended early slices remain: discovery seam, dossier artifact and persistence, planner-on-dossier boundary, stream-card artifact and compiler, then typed contract failure handling.
- Add objective-local implicit knowledge capture early, but keep it narrow at first: capture the signals, normalize them, and make them available for future contract patch compilation before attempting global learning.
- This initiative is justified directly by recent benchmark evidence: reliability has improved, but reviewer churn still reveals missing architectural contract transfer between planning and execution.
- The target product shape remains aligned with the existing locked design docs: humans keep judgment, Tack makes execution reliable.
