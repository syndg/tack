# Blueprint Model Refactor Plan

**Date:** 2026-04-02  
**Status:** Completed historical implementation plan

- [x] Add blueprint `id` and `default` fields to blueprint schema
- [x] Add `foreach: work_item` support to `blueprint_ref` steps
- [x] Make the registry canonical on blueprint ID, not filename/name aliases
- [x] Resolve the default blueprint from loaded blueprints (single blueprint or explicit default)
- [x] Remove the public simple-mode path from the CLI/API
- [x] Keep public request/response fields on `blueprint`
- [x] Keep public discovery endpoints on `/blueprints`
- [x] Switch nested refs from file paths to blueprint IDs
- [x] Replace shipped defaults with neutral blueprint IDs/names
- [x] Update tests and README for the new blueprint model
- [x] Run `gofmt` and `go test ./...`
