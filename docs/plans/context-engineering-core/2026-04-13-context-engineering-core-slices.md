# Context Engineering Core Slice Breakdown

**Parent PRD:** `docs/plans/context-engineering-core/2026-04-13-context-engineering-core-parent-prd.md`

This document breaks the parent PRD into independently grabbable vertical-slice PRDs. Each slice is intended to be narrow, end-to-end, and verifiable on its own.

## Slice Overview

### Slice 1: Discovery seam and persisted dossier MVP

- Type: `AFK`
- Blocked by: none
- Deliverable: every objective runs through a first-class discovery seam before planning and persists a dossier artifact with compact cited context and explicit repo priors
- User stories covered: 1, 3, 5, 6, 7, 14, 22, 25

### Slice 2: Planner-on-dossier boundary and dossier expansion

- Type: `AFK`
- Blocked by: Slice 1
- Deliverable: planner stops exploring code directly, consumes dossier input only, and can return `needs_dossier_expansion`
- User stories covered: 2, 6, 7, 8, 21, 22

### Slice 3: Persisted stream cards and contract-driven builder input

- Type: `AFK`
- Blocked by: Slice 2
- Deliverable: planner emits persisted stream cards with acceptance criteria, hard anchors, and citations; builders consume rendered contracts from those cards
- User stories covered: 9, 10, 11, 12, 13, 14, 15

### Slice 4: Reviewer contract checks and typed contract failures

- Type: `AFK`
- Blocked by: Slice 3
- Deliverable: reviewer validates against stream-card contract only, and execution distinguishes `contract_blocked` from `contract_gap` with localized repair routing
- User stories covered: 16, 17, 18, 19, 20

### Slice 5: Objective insight log and contract-patch foundation

- Type: `AFK`
- Blocked by: Slice 4
- Deliverable: objective-local implicit knowledge is captured from review/retry/approval flows and becomes the foundation for future contract patches and codification candidates
- User stories covered: 24, 25, 26, 27, 28, 29, 30

## Why This Breakdown

The order is intentional.

Slice 1 creates the persisted research artifact and workflow seam the rest of the system depends on.

Slice 2 makes the planner cleanly dependent on dossier context rather than direct repo exploration.

Slice 3 turns planner output into explicit stream-card contracts that builders can follow narrowly.

Slice 4 tightens reviewer semantics and introduces typed contract failure handling so missing constraints stop masquerading as generic builder failures.

Slice 5 adds objective-local learning so repeated findings survive across retries and can later be promoted into stronger harness primitives.

## Notes

- These slices are intentionally more vertical than purely module-based. Each one is expected to touch persistence, workflow, artifact rendering, and tests where necessary.
- All five slices are marked `AFK` because the parent architectural decisions are already sufficiently locked for implementation. If new major ambiguities appear during coding, they can be escalated in follow-up design notes rather than changing this initial breakdown.
- Future follow-on slices can extend from Slice 5 into codification candidates, reviewable promotion flows, and later memory distillation once objective-local learning is proven useful.
