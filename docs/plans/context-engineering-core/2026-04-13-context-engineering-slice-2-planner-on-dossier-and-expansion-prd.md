# Slice PRD: Planner On Dossier And Dossier Expansion

## Parent PRD

`docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

## Problem Statement

Even with a persisted dossier, planning quality will remain muddled if the planner is still allowed to explore the repo directly. That keeps discovery and planning entangled, makes planning quality harder to evaluate, and lets planner-side wandering reintroduce the same context ambiguity this initiative is trying to remove.

The next missing capability is a hard boundary: planner should consume dossier context only, and when the dossier is insufficient, it should explicitly request more discovery rather than silently doing more exploration itself.

## Solution

Make the planner dossier-driven and strict. The planner should consume the objective, the persisted dossier, and workflow constraints only. It should not perform fresh code exploration.

If the dossier is insufficient for trustworthy decomposition, the planner should return `needs_dossier_expansion`. Discovery can then deepen the dossier and planning can retry. The dossier may suggest likely seams, and the planner may override those seams, but must explain why.

This slice is successful when planning quality can be evaluated as planning quality rather than hidden discovery work.

## User Stories

1. As a developer, I want the planner to stop exploring the repo directly, so that planning and discovery are separate concerns.
2. As a developer, I want the planner to consume the dossier as its main context source, so that decomposition is grounded in researched context.
3. As a developer, I want the planner to request dossier expansion when context is insufficient, so that weak plans do not come from weak inputs.
4. As a developer, I want dossier-proposed seams to influence planning without fully constraining it, so that planner judgment remains auditable.
5. As a developer, I want planner seam overrides to be justified explicitly, so that decomposition changes are legible.

## Implementation Decisions

- Remove planner repo exploration from the planning workflow.
- Make dossier the required planner input.
- Add `needs_dossier_expansion` as a planner-side typed outcome.
- Route dossier expansion back through discovery rather than planner self-exploration.
- Preserve planner authority over final decomposition, while requiring explicit rationale for seam overrides.

## Testing Decisions

- Test that planner consumes dossier artifacts and not raw repo exploration.
- Test the `needs_dossier_expansion` path.
- Test planner seam override rationale behavior.
- Use deterministic fixtures that make insufficient-dossier vs sufficient-dossier outcomes easy to observe.

## Out of Scope

- Persisted stream-card schema.
- Builder/reviewer contract enforcement.
- Objective insight log capture from retries/reviews.

## Further Notes

- This slice creates the clean architecture boundary that the benchmark work was missing.
- It should be easy after this slice to tell whether a poor plan came from bad discovery or bad decomposition.
