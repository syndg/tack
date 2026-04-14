# Context Engineering Core

This folder contains the parent PRD and the initial child slice PRDs for Tack's current context-engineering implementation initiative.

## Reading Order

1. `2026-04-13-context-engineering-core-parent-prd.md`
2. `2026-04-13-context-engineering-core-slices.md`
3. `2026-04-13-context-engineering-slice-1-discovery-and-persisted-dossier-prd.md`
4. `2026-04-13-context-engineering-slice-2-planner-on-dossier-and-expansion-prd.md`
5. `2026-04-13-context-engineering-slice-3-stream-cards-and-contract-rendering-prd.md`
6. `2026-04-13-context-engineering-slice-4-typed-contract-failures-and-local-replanning-prd.md`
7. `2026-04-13-context-engineering-slice-5-objective-insight-log-and-contract-patch-foundation-prd.md`

## What Lives Here

- The parent PRD defines the locked architectural direction.
- The slice breakdown doc explains the vertical-slice ordering and dependencies.
- The slice PRDs define independently shippable implementation slices.

## Recommended Implementation Order

1. Slice 1: discovery seam and persisted dossier MVP
2. Slice 2: planner-on-dossier boundary and dossier expansion
3. Slice 3: persisted stream cards and contract rendering
4. Slice 4: typed contract failures and local replanning
5. Slice 5: objective insight log and contract-patch foundation

## Current Notes

- The architectural decisions for these slices are considered locked enough for implementation.
- The benchmark work that motivated these docs is recorded separately under `docs/benchmarks/`.
- Objective-local context comes first. Objective-local codification candidates can now be derived, but broad cross-objective memory and automatic promotion remain follow-on work.
