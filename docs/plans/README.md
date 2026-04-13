# Plans Index

Use this directory for design docs and implementation plans that still matter to the current codebase.

Not every file here has the same weight. Use the status marker at the top of each doc.

## How To Read This Directory

- `Draft` or `Planned`
  Active direction or upcoming work.
- `Active design reference / partially implemented`
  Still relevant to current implementation work. Use these as the deeper internal reference.
- `Implemented foundation / historical design note`
  Important background for current architecture, but the feature has already largely landed.
- `Completed historical implementation plan`
  Finished checklist kept for history.
- `Superseded`
  Historical only. Prefer the replacement doc.
- `Deferred future work`
  Intentionally parked. Relevant, but not being built now.

## Current Priority Docs

- `2026-04-02-context-engineering-and-harness-hardening-design.md`
  Current product direction for context engineering and harness hardening.
- `2026-04-09-retry-recovery-design.md`
  Recovery design reference for the self-healing execution model.
- `2026-04-09-retry-recovery-plan.md`
  Working implementation backlog for recovery redesign.
- `2026-04-05-operator-observability-plan.md`
  Planned observability refactor for a deeper operator record model.
- `2026-04-09-feature-resurrection-benchmark-design.md`
  Deferred evaluation idea that should shape future context work.
- `2026-04-10-adaptive-deliberation-and-dossier-design.md`
  Product design for adaptive planning depth, dossiers, and simple UX.
- `2026-04-11-lazygit-benchmark-v1-design.md`
  Concrete first benchmark spec using lazygit and a feature-resurrection slice.
- `context-engineering-core/README.md`
  Entry point for the parent PRD and child slice PRDs for the current context-engineering implementation initiative.

## Current Architecture Background

- `2026-04-02-blueprint-model-design.md`
- `2026-04-04-multi-project-daemon-design.md`
- `2026-03-14-gate-isolation-design.md`
- `2026-03-17-sandbox-reuse-design.md`
- `2026-03-14-observability-partial-completion-design.md`
- `2026-03-13-nested-sub-execution-design.md`

These are not the current product story, but they explain why the current code looks the way it does.

## Historical And Test-Specific Docs

- `2026-03-13-ledger-e2e-test-design.md`
  Historical test-project design for earlier core-loop validation.
- `2026-03-13-agent-comm-design.md`
  Superseded by gate-isolation simplification.
- `2026-02-25-orchestrator-design.md`
  Large early design that still contains useful principles, but no longer matches the current product shape in many details.

When in doubt, start with `docs-site/content/docs/`, then `docs/CURRENT_ARCHITECTURE.md`, then the active docs listed above.
