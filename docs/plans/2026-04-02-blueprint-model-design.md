# Blueprint Model Redesign

**Date:** 2026-04-02  
**Status:** Implemented foundation / historical design note

This doc captures the blueprint identity and composition model that now underpins the current public docs. For current user-facing behavior, also see `docs-site/content/docs/concepts/blueprints.mdx`.

## Goal

Make Tack blueprints fully user-owned without forcing users to learn or preserve internal starter concepts like `feature`, `hotfix`, or `stream`.

## Product Model

A blueprint is the user-facing orchestration concept.

Each blueprint:
- lives in `.tack/blueprints/*.yaml`
- has an explicit machine `id`
- may have a human `name`
- may mark itself `default: true`
- references other blueprints by `id`

Filenames are organizational only. They are not part of the public API.

## Selection Rules

- If an objective explicitly requests a blueprint, use that blueprint ID.
- Otherwise:
  - if exactly one blueprint is loaded, use it
  - if multiple blueprints are loaded, exactly one must have `default: true`
  - if none are default, return an error requiring explicit blueprint selection
  - if multiple are default, fail validation/loading

## Composition Model

Blueprint references are explicit by ID:

```yaml
- id: execute
  type: blueprint_ref
  ref: build-review
  foreach: work_item
```

`foreach: work_item` is the user-facing term for fanout over planned units of work. This replaces leaked internal language like `stream`.

## User-Facing API Changes

- CLI flag is `--blueprint`
- objective creation request field is `blueprint`
- blueprint discovery endpoints are `/blueprints`
- objective JSON field is `blueprint`
- execution JSON field is `blueprint_id`

Internal packages may continue using the `blueprint` package name during this refactor.

## Non-Goals

- No compatibility layer for old `feature` / `hotfix` / `stream` conventions
- No config routing slots like `default`, `simple`, or `per_stream`
- No path-based blueprint references like `.tack/blueprints/stream.yaml`

## Initial Shipped Defaults

Ship neutral defaults instead of role-taxonomy names:
- one top-level default blueprint
- one reusable nested blueprint for per-work-item execution

Example IDs:
- `standard`
- `build-review`
