# Benchmark Telemetry

- Benchmark run: `238cc924-33f4-4739-8927-6d34888a24a8`
- Daemon run: `cc0cc8b9-a6c9-40b2-a02a-1ac2b639fa29`
- Benchmark: `lazygit.command-log-nav-keybindings`
- Benchmark status: `planned`
- Daemon run status: `active`
- Objective status: `executing`
- Plan status: `approved`

## Summary

- Streams: `2 total`, `0 merged`, `1 failed`, `1 non-terminal`
- Stream executions: `1`
- Builder sessions: `1`
- Reviewer sessions: `1`
- Recovery ledger entries: `1`
- Review rejections: `0`
- Automatic recovery decisions: `1`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-13T14:45:23Z`
- Finished: `2026-04-13T14:53:06Z`
- Duration: `7m43s`

## Quality Gates

- `go test ./pkg/gui/...`
- `go generate ./...`
- `go run cmd/integration_test/main.go cli ui/command_log_navigation`

## Notes

- Objective inferred from single objective present in daemon data.

## Streams

### `ae17d623-1a03-4790-a971-ef06d5c85464` Regression coverage for command log paging and jumping

- Status: `pending`
- Stream executions: `0`
- Builder sessions: `0`
- Reviewer sessions: `0`
- Recovery ledger entries: `0`
- Review rejections: `0`
- Automatic recovery decisions: `0`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `0`

### `7085b84a-5a65-4167-a457-31279b00f492` Focused command log navigation behavior

- Status: `failed`
- Stream executions: `1`
- Builder sessions: `1`
- Reviewer sessions: `1`
- Recovery ledger entries: `1`
- Review rejections: `0`
- Automatic recovery decisions: `1`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-13T14:48:51Z`
- Finished: `2026-04-13T14:53:06Z`
- Duration: `4m15s`
