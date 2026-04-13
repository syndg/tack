# Slice PRD: Typed Contract Failures And Local Replanning

## Parent PRD

`docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

## Problem Statement

Even with better stream cards, the system still needs to distinguish execution mistakes from contract mistakes. Right now review churn often collapses both into generic review rejection behavior. That makes builders absorb blame for missing architecture and causes reviewers to keep acting like latent planners.

The missing capability is typed contract failure handling and local repair semantics.

## Solution

Introduce explicit failure outcomes for at least `contract_blocked` and `contract_gap`.

Builders should return `contract_blocked` when the stream card is contradictory or insufficient to proceed safely. Reviewers should return `contract_gap` when the stream card and dossier-backed constraints are missing a necessary requirement that was not part of the builder's contract.

`contract_gap` should trigger localized repair on the affected stream card only, preserving the rest of the plan unless dependency logic forces change. Reviewer overlays and routing logic should reinforce that reviewers validate the contract rather than inventing new architecture outside it.

This slice is successful when missing contract detail stops masquerading as ordinary builder failure.

## User Stories

1. As a developer, I want builders to signal when the contract is unusable, so that Tack stops forcing them to guess.
2. As a developer, I want reviewers to distinguish builder failures from missing contract constraints, so that execution quality is judged fairly.
3. As a developer, I want `contract_gap` to replan only the affected stream, so that the rest of the objective remains stable.
4. As a developer, I want reviewer semantics to stay contract-bound, so that reviewers stop acting like hidden planners.
5. As an operator, I want contract failure types persisted and inspectable, so that retry behavior and future reporting become more legible.

## Implementation Decisions

- Add typed outcomes for `contract_blocked` and `contract_gap`.
- Teach builders to fail fast with `contract_blocked` rather than improvising around bad contracts.
- Teach reviewers to validate only against stream-card contract and dossier-backed constraints.
- Route `contract_gap` into localized stream-card repair or replanning.
- Persist contract failure states as first-class run data rather than reducing them to free-form rejection strings.

## Testing Decisions

- Test builder-side `contract_blocked` routing.
- Test reviewer-side `contract_gap` routing.
- Test localized replanning preserves unaffected streams.
- Test that generic review rejection behavior is not used when a typed contract failure is more accurate.
- Use end-to-end flow tests with representative artificial contract gaps.

## Out of Scope

- Cross-objective learning or codification.
- Broad redesign of all recovery types outside the contract-failure path.

## Further Notes

- This slice is where the benchmark lesson becomes operational: reviewers should stop discovering architecture and silently turning it into builder churn.
