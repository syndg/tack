# Slice PRD: Stream Cards And Contract Rendering

## Parent PRD

`docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

## Problem Statement

Today, streams are still too close to enriched prose. Builders and reviewers do not receive a precise, durable, issue-like contract with the same structure and the same cited constraints. That leaves too much room for under-specified implementation, scope drift, and reviewer rediscovery of architecture-level requirements.

The next missing capability is a first-class stream-card artifact that converts planner output into a durable execution contract.

## Solution

Make persisted stream cards the planner's first-class output and the builder's execution contract source.

Each stream card should include title, goal, blocked-by relationships, acceptance criteria, implementation scope, proof scope, hard implementation anchors, and citations for those anchors. The planner may also record rationale when it overrides dossier-proposed seams.

Builder-facing overlays should render directly from stream cards rather than from loose stream descriptions. Builders should be told to execute the card narrowly and not reinterpret architecture.

This slice is successful when a builder can receive a precise, cited, bounded contract from persisted planner output.

## User Stories

1. As a developer, I want streams represented as explicit cards, so that execution contracts are durable and legible.
2. As a developer, I want each card to include acceptance criteria, so that correctness is explicit before execution starts.
3. As a developer, I want each card to include implementation scope and proof scope, so that builders know both what to build and what evidence to provide.
4. As a developer, I want hard implementation anchors in each card, so that repo-specific architectural constraints are front-loaded rather than rediscovered in review.
5. As a developer, I want every hard implementation anchor to cite dossier evidence, so that the builder can trust where the constraint came from.
6. As a developer, I want the builder overlay to render from the card, so that planner intent reaches execution without extra prompt drift.

## Implementation Decisions

- Add a first-class persisted stream-card artifact distinct from stream description prose.
- Make planner emit stream cards as structured outputs.
- Render builder execution overlays from stream-card data.
- Treat implementation anchors as hard constraints by default.
- Require citation support for hard anchors.
- Keep stream descriptions as compatibility text only if needed, but make stream cards the source of truth.

## Testing Decisions

- Test stream-card persistence and retrieval.
- Test planner output compilation into valid stream cards.
- Test builder overlay rendering from stream-card fields.
- Test that hard anchors carry citations into execution-facing packets.
- Validate on fixtures where acceptance criteria and architectural anchors differ from each other.

## Out of Scope

- Reviewer-side contract enforcement.
- Typed contract failures and localized replanning.
- Objective insight log and codification pathways.

## Further Notes

- This slice should make benchmark stream contracts much more explicit than current acceptance-criteria markers embedded in prose.
- Builder behavior should remain intentionally dumb after this slice, but reviewer semantics can stay mostly unchanged until the next slice.
