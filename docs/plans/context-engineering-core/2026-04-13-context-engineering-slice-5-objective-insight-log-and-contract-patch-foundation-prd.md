# Slice PRD: Objective Insight Log And Contract Patch Foundation

## Parent PRD

`docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

## Problem Statement

Even with better contracts and typed failures, Tack still loses too much implicit knowledge during a run. Reviewer findings, dossier edits, plan approval rationale, retries with human guidance, and repeated corrections are all high-value signals. Today, many of those signals survive only as scattered text in attempts, comments, or transient prompts.

The missing capability is a durable objective-local learning artifact that helps the current objective get smarter without pretending to be global memory.

## Solution

Introduce an append-only persisted `Objective Insight Log` that captures normalized objective-local signals from the run lifecycle.

The first sources should be reviewer rejections, human retry guidance, dossier edits, and plan approval or edit rationale. Those signals should be stored in normalized form and be available to future retry overlays, localized replanning, and later derived contract patches.

This slice should also establish the foundation for later `Contract Patches` and `Codification Candidates`, but keep those promotion paths narrow and reviewable. The system should learn within the current objective first before promoting anything broader.

This slice is successful when repeated corrections stop being ephemeral and start becoming durable local intelligence.

## User Stories

1. As a developer, I want repeated reviewer findings to be preserved within the current objective, so that retries and replanning do not start from scratch.
2. As a developer, I want human retry guidance to become durable objective-local context, so that later execution can incorporate it explicitly.
3. As a developer, I want dossier edits and plan approval rationale captured, so that important human judgment survives beyond a single interaction.
4. As a developer, I want objective-local insights to be compilable into contract patches later, so that the system can strengthen the stream contract without free-form prompt stuffing.
5. As an operator, I want repeated normalized insights to become codification candidates later, so that stable patterns can graduate into rules or checks after human review.
6. As an operator, I want this intelligence to remain objective-local first, so that Tack does not prematurely invent unsafe global memory.

## Implementation Decisions

- Add a first-class persisted `Objective Insight Log`.
- Capture normalized signals from reviewer rejections, human retry guidance, dossier edits, and plan approval or edit paths.
- Keep the insight log append-only and objective-scoped.
- Separate explicit repo priors from learned objective-local insights.
- Make future contract patches derive from normalized insight data rather than raw concatenated text.
- Make future codification candidates passive and reviewable rather than auto-promoted.

## Testing Decisions

- Test insight capture from review and retry flows first.
- Test insight capture from dossier edits and plan approval rationale.
- Test normalized persistence and retrieval behavior.
- Test that retry/replanning consumers can read objective-local insights without relying on free-form prompt transcripts.
- Keep future contract-patch and codification tests narrow until those derived layers are implemented.

## Out of Scope

- Global memory across objectives.
- Automatic promotion of insights into repo-wide rules or lints.
- Full UI work for browsing or managing insight logs.

## Further Notes

- This slice should stay objective-local by design.
- It is the preferred starting point for implicit knowledge capture because it collects signals before trying to codify them.
