# Slice PRD: Discovery Seam And Persisted Dossier MVP

## Parent PRD

`docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

## Problem Statement

Tack still begins planning with too little structured research. Even when the system already has rules, nearby docs, and obvious code precedents, that information is not yet being assembled into a durable artifact before planning starts. As a result, planning quality depends too much on direct planner exploration and too little on a deliberate research step.

The first missing capability is not a full memory system. It is a first-class discovery seam and a persisted dossier artifact. Without those, the rest of the context-engineering architecture has no stable input.

## Solution

Introduce a first-class discovery workflow that runs before planning on every objective and produces a persisted dossier artifact.

The dossier should capture compact objective-local research using deterministic retrieval plus a synthesis step. It should include objective summary, relevant files and modules, repo priors such as path-scoped rules and workflow constraints, similar implementations, risks and unknowns, suggested seams, and citations. Humans should be able to inspect and edit the dossier, but the system should auto-proceed into planning by default.

This slice is successful when dossier generation is a real end-to-end workflow seam rather than a planning side effect.

## User Stories

1. As a developer, I want every objective to pass through a discovery step before planning, so that planning starts from researched context.
2. As a developer, I want Tack to persist the dossier from day one, so that the context behind planning is inspectable and durable.
3. As a developer, I want the dossier to include cited repo facts and priors, so that later artifacts can rely on concrete evidence.
4. As a developer, I want to inspect and edit the dossier, so that important context can be corrected before planning if needed.
5. As a developer, I want dossier generation to auto-run by default, so that routine workflows stay low-friction.
6. As an operator, I want dossier persistence to be objective-scoped, so that context stays local rather than becoming fake global memory.

## Implementation Decisions

- Add a first-class discovery workflow boundary before planning.
- Persist dossier artifacts as objective-local records from day one.
- Include explicit repo priors such as existing rules and blueprint constraints in the dossier.
- Use deterministic retrieval plus synthesis for dossier generation.
- Keep dossier structure compact, cited, and planner-facing.
- Support editing the dossier after generation, but do not require explicit human approval to continue by default.
- Keep this slice focused on dossier production and persistence, not yet on making planner behavior strict.

## Testing Decisions

- Test dossier creation for new objectives end-to-end.
- Test dossier persistence and retrieval behavior.
- Test dossier editing and update behavior.
- Test that repo priors and citations are present in dossier outputs for representative fixtures.
- Prefer tests that assert on dossier artifact shape and workflow transitions rather than prompt text.

## Out of Scope

- Making planner consume the dossier exclusively.
- Stream-card output.
- Typed contract failure handling.
- Objective insight logging beyond dossier persistence and edits.

## Further Notes

- This slice creates the artifact boundary required by every later slice.
- If a compact summary/index is needed for observability, it should be derived from the persisted dossier rather than replacing it.
