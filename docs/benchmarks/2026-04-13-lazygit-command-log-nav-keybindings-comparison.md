# Benchmark Comparison: lazygit.command-log-nav-keybindings

**Date:** 2026-04-13  
**Benchmark spec:** `lazygit.command-log-nav-keybindings`

## Runs Compared

### Initial run (`local12`)

- Benchmark run record: `bb0b9f1d-cef3-40fc-b47a-7351c1d61206`
- Daemon run: `4b8a7317-9c62-46fa-b4f7-e789c6dffc42`
- Result: partial / incomplete

### Patched rerun (`local13`)

- Benchmark run record: `cef3d5bf-8830-4f11-8eda-ec498dc908eb`
- Daemon run: `eb8e8bdb-29df-47e1-9737-0146f60db6b7`
- Result: both streams merged successfully

### Fresh rerun after reliability fixes (`local16`)

- Benchmark run record: `6d97f68d-2abb-406a-858a-f0566aadf4de`
- Daemon run: `32743176-bc2b-48b3-8676-06e9f94b6e58`
- Result: all three streams merged successfully
- Canonical telemetry report: `docs/benchmarks/2026-04-13-lazygit-command-log-nav-keybindings-6d97f68d-2abb-406a-858a-f0566aadf4de.md`

## Harness Differences In `local16`

This rerun was not just another retry. It included additional harness fixes beyond the `local13` patched rerun:

- benchmark plan quality-gate override now waits long enough for slow planner runs, so the benchmark-safe gate was actually applied before approval
- post-merge gate failures can now be retried even when the linked stream sub-execution was already marked `completed`
- review rejection fix context now preserves earlier concrete reviewer findings instead of letting later broader rejections overwrite them
- reviewer overlay explicitly tells reviewers to preserve unresolved concrete findings verbatim across reruns
- run/objective finalization remained correct on completion

## Outcome Summary

### Initial run (`local12`)

- Streams merged: `2/3`
- Final state: stalled partial run
- Main failure mode: one coverage/proof stream exhausted after repeated review churn

### Patched rerun (`local13`)

- Streams merged: `2/2`
- Final state: feature benchmark completed for the narrowed 2-stream plan
- Main remaining weakness: one human-guided resume still required

### Fresh rerun (`local16`)

- Streams merged: `3/3`
- Final state: objective and daemon run both completed cleanly
- Human-guided resumes: `2`
- Stream 1 `Command log scroll mechanics`: merged after one guided resume
- Stream 2 `Scoped command log keybindings and panel affordances`: merged after one guided resume
- Stream 3 `Regression coverage and keybinding reference sync`: merged autonomously in one pass

## Captured Telemetry

### Initial run telemetry

- Streams: `3 total`, `2 merged`, `1 failed`, `0 non-terminal`
- Stream executions: `4`
- Builder sessions: `9`
- Reviewer sessions: `9`
- Recovery ledger entries: `8`
- Review rejections: `8`
- Automatic recovery decisions: `5`
- Human escalations: `2`
- Human resumes: `1`
- Human guidance provided: `1`
- Human guidance required: `yes`
- Merge attempts: `2`
- Duration: `55m21s`

### Patched rerun telemetry

- Streams: `2 total`, `2 merged`, `0 failed`, `0 non-terminal`
- Stream executions: `3`
- Builder sessions: `6`
- Reviewer sessions: `6`
- Recovery ledger entries: `5`
- Review rejections: `5`
- Automatic recovery decisions: `3`
- Human escalations: `1`
- Human resumes: `1`
- Human guidance provided: `1`
- Human guidance required: `yes`
- Merge attempts: `2`
- Duration: `34m56s`

### Fresh rerun telemetry

- Streams: `3 total`, `3 merged`, `0 failed`, `0 non-terminal`
- Stream executions: `5`
- Builder sessions: `11`
- Reviewer sessions: `11`
- Recovery ledger entries: `10`
- Review rejections: `10`
- Automatic recovery decisions: `6`
- Human escalations: `2`
- Human resumes: `2`
- Human guidance provided: `2`
- Human guidance required: `yes`
- Merge attempts: `3`
- Duration: `1h1m42s`

## What Improved

### 1. Reliability improved materially

- `local16` completed end-to-end with all streams merged
- benchmark-safe gates held correctly on the live run: `go test ./pkg/gui/... -count=1`
- aggregate objective/run rows finished correctly without the stale finalization problem seen earlier
- `benchmark execute` returned cleanly instead of leaving benchmark run linkage stale

### 2. Recovery behavior improved

- the system recovered from a post-merge validation failure mode that previously wedged retry handling
- blocked review loops now carried forward earlier concrete findings instead of reducing them to a broader, less actionable restatement

### 3. Final stream sequencing worked as intended

- stream 3 stayed pending until the earlier dependency chain completed
- after stream 2 merged, stream 3 started and merged cleanly in one execution without additional review churn

## What Did Not Improve Enough

### 1. Autonomy is still not good enough

- `local16` still required `2` human-guided resumes
- both implementation-heavy streams exhausted into `ask_human_then_resume`
- review churn remained high: `10` review rejections across the run

### 2. Contract fidelity is still the main bottleneck

- the acceptance criteria transfer is better than `local12`, but reviewers still surfaced architecture-level constraints late
- stream 1 eventually required explicit guidance about session-scoped autoscroll behavior
- stream 2 eventually required explicit guidance about removing imperative binding injection from `extras_panel.go` and routing ownership fully through `CommandLogController`

### 3. Planner decomposition changed run hardness

- `local13` used a tighter 2-stream plan
- `local16` used a 3-stream plan again:
  - `Command log scroll mechanics`
  - `Scoped command log keybindings and panel affordances`
  - `Regression coverage and keybinding reference sync`
- that makes direct autonomy comparisons imperfect: `local16` completed more work, but also incurred more review churn and longer wall-clock time

## Assessment

The `local16` run is the strongest reliability result so far for this benchmark family.

It demonstrates that the recent harness fixes are real and measurable:

- safe benchmark gates stayed enforced
- retry/recovery paths handled more failure shapes correctly
- run finalization completed correctly
- the benchmark converged fully instead of stalling partial

But it also shows that reliability fixes alone do not solve the planner/builder/reviewer contract-transfer problem.

The remaining weakness is not raw orchestration anymore. It is that the system still discovers too much of the true architectural contract through repeated reviewer rejection instead of front-loading it into the builder's first execution.

## Practical Takeaway

Current state for `lazygit.command-log-nav-keybindings`:

- orchestration reliability: much better
- benchmark validity: fixed again
- completion rate: improved to full completion on `local16`
- autonomy quality: still weak
- next highest-leverage work: better upfront transfer of codebase-specific implementation constraints, not just behavior-level acceptance criteria
