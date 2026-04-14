# Benchmark Telemetry

- Benchmark run: `0a9e19e7-60d0-459e-8317-b112770cb905`
- Daemon run: `d5d888b3-bf12-4d4b-8353-44f6fb156f87`
- Benchmark: `lazygit.undo-basic-commit-checkout`
- Benchmark status: `completed`
- Daemon run status: `completed`
- Objective status: `completed`
- Plan status: `completed`

## Summary

- Streams: `3 total`, `3 merged`, `0 failed`, `0 non-terminal`
- Stream executions: `3`
- Builder sessions: `5`
- Reviewer sessions: `4`
- Recovery ledger entries: `2`
- Review rejections: `1`
- Automatic recovery decisions: `2`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `3`
- Started: `2026-04-14T01:32:23Z`
- Finished: `2026-04-14T02:03:27Z`
- Duration: `31m4s`

## Quality Gates

- `go test ./pkg/gui/controllers -count=1`
- `go test ./pkg/commands/git_commands -count=1`

## Streams

### `11376db3-48e0-4c53-8582-f3eeecda393c` Narrow reflog undo core to plain commit and checkout

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
- Started: `2026-04-14T01:33:52Z`
- Finished: `2026-04-14T01:41:34Z`
- Duration: `7m42s`

### `e9712cbb-5538-4266-8dd6-021c9b15a11d` Exercise supported and unsupported undo flows in integration tests

- Status: `merged`
- Stream executions: `1`
- Builder sessions: `2`
- Reviewer sessions: `1`
- Recovery ledger entries: `1`
- Review rejections: `0`
- Automatic recovery decisions: `1`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T01:41:36Z`
- Finished: `2026-04-14T02:03:26Z`
- Duration: `21m50s`

### `05938809-7e0f-4b04-9f35-3a2fdded1e42` Update user-facing docs and copy for the benchmark slice

- Status: `merged`
- Stream executions: `1`
- Builder sessions: `2`
- Reviewer sessions: `2`
- Recovery ledger entries: `1`
- Review rejections: `1`
- Automatic recovery decisions: `1`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T01:41:36Z`
- Finished: `2026-04-14T01:47:52Z`
- Duration: `6m16s`
