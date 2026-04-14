# Benchmark Telemetry

- Benchmark run: `545e5673-1b4a-4527-b62a-da2542e08d6a`
- Daemon run: `e27c13e3-24ca-46a1-8f57-fb85222aab0f`
- Benchmark: `lazygit.undo-basic-commit-checkout`
- Benchmark status: `completed`
- Daemon run status: `completed`
- Objective status: `completed`
- Plan status: `completed`

## Summary

- Streams: `3 total`, `3 merged`, `0 failed`, `0 non-terminal`
- Stream executions: `3`
- Builder sessions: `7`
- Reviewer sessions: `7`
- Recovery ledger entries: `5`
- Review rejections: `4`
- Automatic recovery decisions: `5`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `3`
- Final validation: `passed`
- Started: `2026-04-14T08:27:57Z`
- Finished: `2026-04-14T09:34:50Z`
- Duration: `1h6m53s`

## Quality Gates

- `go test ./pkg/gui/controllers -count=1`
- `go test ./pkg/commands/git_commands -count=1`

## Final Validation

- Command: `go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v`
- Status: `passed`
- Checked: `2026-04-14T09:34:50Z`
- Summary: Final benchmark validation passed.
- Detail: Revalidated with headless pty-backed integration tests: TestIntegration/undo/undo_commit and TestIntegration/reflog/checkout both passed.

## Note

- This report supersedes the initial terminal `partial` result for the same run. The original benchmark validation command used `go run cmd/integration_test/main.go cli ...`, which requires a real tty and fails headlessly with `/dev/tty: device not configured` even on lazygit's canonical feature commit. The run was revalidated against the preserved merge worktree using lazygit's pty-backed headless integration path, and the benchmark result was corrected to `completed` / `passed`.

## Streams

### `b7223f54-190e-4054-b8b4-6ebac4a778e8` Narrow reflog undo core to plain commit and checkout

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
- Started: `2026-04-14T08:29:15Z`
- Finished: `2026-04-14T09:24:57Z`
- Duration: `55m42s`

### `ce0cd96f-67c4-4769-b854-91f5ec43916f` Exercise supported and unsupported undo flows in integration tests

- Status: `merged`
- Stream executions: `1`
- Builder sessions: `2`
- Reviewer sessions: `2`
- Recovery ledger entries: `2`
- Review rejections: `1`
- Automatic recovery decisions: `2`
- Human escalations: `0`
- Human resumes: `0`
- Human guidance provided: `0`
- Human guidance required: `no`
- Merge attempts: `1`
- Started: `2026-04-14T09:24:58Z`
- Finished: `2026-04-14T09:34:50Z`
- Duration: `9m52s`

### `a92b3943-ae6c-491d-b5e1-51aece2ffd78` Update user-facing docs and copy for the benchmark slice

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
- Started: `2026-04-14T09:24:58Z`
- Finished: `2026-04-14T09:31:48Z`
- Duration: `6m50s`
