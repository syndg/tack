# Docs Guide

This repo currently has two documentation layers:

- `docs-site/content/docs/` is the current public-facing product story. Start here when you want the latest high-level description of what Tack is, what exists, and what direction is current.
- The code under `cmd/` and `internal/` is the implementation source of truth.

Everything else in `docs/` should be read with more context.

## What Lives Where

- `docs-site/content/docs/`
  Current product docs. Best starting point for the current Tack model, CLI, concepts, and guides.
- `docs/CURRENT_ARCHITECTURE.md`
  Code-backed internal map of the current implementation. Use this when internal docs drift and you need a fast way to re-anchor on the actual structure.
- `docs/plans/`
  Active design docs and implementation plans. Use this for proposals, upcoming work, and design direction that is still being shaped.
- `docs/plans/README.md`
  Index for the plans directory. Use this to find the active docs faster and distinguish active references from historical notes.
- `docs/DEFERRED.md`
  Future work and intentionally postponed items. Use this as the backlog for ideas that matter but are not being built right now.

## Recommended Exploration Order

When trying to understand the current state of Tack, use this order:

1. Read `docs-site/content/docs/index.mdx` and the key concept pages in `docs-site/content/docs/concepts/`.
2. Read `docs/CURRENT_ARCHITECTURE.md` to map the public story onto the current code layout.
3. Check `cmd/tack/` for the actual CLI surface (`plan`, `approve`, `watch`, `mail`, `project`, `init`, `merge`).
4. Check `internal/daemon/` for daemon routes and project-scoped execution wiring.
5. Check `internal/harness/` for blueprint, gates, rules, and tool-shaping infrastructure.
6. Check `internal/services/` for planner, dispatch, lifecycle, merge, runs, mail, and agent orchestration behavior.
7. Check `internal/recovery/`, `internal/sandbox/`, `internal/runtime/`, and `internal/config/` for cross-cutting execution behavior.
8. Read `docs/plans/` for active design direction.
9. Read `docs/DEFERRED.md` for future work that is intentionally parked.

## Doc Hygiene Rules

- If the public product story changes, update `docs-site/content/docs/`.
- If a feature is being actively designed, add or update a doc in `docs/plans/`.
- If an idea matters but is intentionally postponed, add it to `docs/DEFERRED.md` and link to a design doc if one exists.
- When internal docs and docs-site disagree, trust the code first, then update the docs-site and this guide so the disagreement does not persist.
