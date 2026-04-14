# Benchmark Telemetry

- Benchmark run: `3414263b-4058-4f4b-a7eb-8d03f54c40c1`
- Daemon run: `87bf007c-9ec4-40a6-b384-8d54844de257`
- Benchmark: `lazygit.undo-basic-commit-checkout`
- Benchmark status: `partial`
- Daemon run status: `partial`
- Objective status: `partial`
- Plan status: `completed`

## Summary

- Streams: `3 total`, `2 merged`, `1 failed`, `0 non-terminal`
- Stream executions: `3`
- Builder sessions: `9`
- Reviewer sessions: `6`
- Recovery ledger entries: `7`
- Review rejections: `3`
- Automatic recovery decisions: `7`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `3`
- Final validation: `failed`
- Started: `2026-04-14T02:21:37Z`
- Finished: `2026-04-14T03:24:26Z`
- Duration: `1h2m49s`

## Quality Gates

- `go test ./pkg/gui/controllers -count=1`
- `go test ./pkg/commands/git_commands -count=1`

## Final Validation

- Command: `go run cmd/integration_test/main.go cli undo/undo_commit reflog/checkout`
- Status: `failed`
- Checked: `2026-04-14T03:24:26Z`
- Summary: Final benchmark validation failed.
- Detail: 2026/04/14 08:54:26 test undo/undo_commit not found. Perhaps you forgot to add it to `pkg/integration/integration_tests/test_list.go`? This can be done by running `go generate ./...` from the Lazygit root. You'll need to ensure that your test name and the file name match (where the test name is in PascalCase and the file name is in snake_case).
exit status 1

## Streams

### `744ef1b8-d90a-477a-8575-b81706bab20e` Narrow reflog undo core to plain commit and checkout

- Status: `merged`
- Stream executions: `1`
- Builder sessions: `1`
- Reviewer sessions: `1`
- Recovery ledger entries: `0`
- Review rejections: `0`
- Automatic recovery decisions: `0`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T02:22:52Z`
- Finished: `2026-04-14T02:29:17Z`
- Duration: `6m25s`

### `538d6127-1184-4a25-bfd1-241960c9727a` Exercise supported and unsupported undo flows in integration tests

- Status: `failed`
- Stream executions: `1`
- Builder sessions: `5`
- Reviewer sessions: `2`
- Recovery ledger entries: `5`
- Review rejections: `1`
- Automatic recovery decisions: `5`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T02:29:18Z`
- Finished: `2026-04-14T03:24:26Z`
- Duration: `55m8s`

### `6d7b3835-1b63-4e6a-9a54-3c24972da1aa` Update user-facing docs and copy for the benchmark slice

- Status: `merged`
- Stream executions: `1`
- Builder sessions: `3`
- Reviewer sessions: `3`
- Recovery ledger entries: `2`
- Review rejections: `2`
- Automatic recovery decisions: `2`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T02:29:18Z`
- Finished: `2026-04-14T02:37:43Z`
- Duration: `8m25s`
