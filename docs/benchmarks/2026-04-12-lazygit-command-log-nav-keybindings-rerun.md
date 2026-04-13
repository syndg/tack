# Benchmark Report: lazygit.command-log-nav-keybindings

**Date:** 2026-04-12  
**Benchmark spec:** `lazygit.command-log-nav-keybindings`

## Runs Compared

### Initial run

- Benchmark run record: `bb0b9f1d-cef3-40fc-b47a-7351c1d61206`
- Daemon run: `4b8a7317-9c62-46fa-b4f7-e789c6dffc42`
- Objective: `febad9c3-6c65-42e0-92a6-b2ccc7f09f5f`
- Plan: `2642bb6b-e8b0-4ea7-ad5e-d4e633d21aae`
- Result: partial / incomplete

### Patched rerun

- Benchmark run record: `cef3d5bf-8830-4f11-8eda-ec498dc908eb`
- Daemon run: `eb8e8bdb-29df-47e1-9737-0146f60db6b7`
- Objective: `5982d959-72a1-4890-9040-6b8fe071ec6e`
- Plan: `20b8182b-a4db-467b-9f13-0930fba30680`
- Result: both streams merged successfully

## What Changed Between Runs

The rerun used a patched harness with explicit per-stream acceptance criteria carried through planning and agent overlays.

Notable harness differences in the rerun:

- Planner emitted `acceptance_criteria` for each stream.
- Those criteria were persisted with the streams and surfaced in agent overlays.
- The rerun plan was narrower and more aligned with the real feature contract.
- The benchmark-safe quality gate remained scoped to `go test ./pkg/gui/... -count=1`.

The rerun still needed one human-guided resume, but the review loop was more targeted and less exploratory than the initial run.

## Outcome Summary

### Initial run

- Streams merged: 2/3
- Failed stream: `81a33862-69de-4f36-aa10-64ad911f601a` (`Focused command log navigation coverage`)
- Human escalations: 2 exhausted review cycles on the same stream
- Final result: coverage stream remained failed; run stalled short of completion

### Patched rerun

- Streams merged: 2/2
- Stream 1 `4cc6948e-f84a-4a3b-9997-1ffe6b49b402`: merged
- Stream 2 `021a0e30-b3be-44a6-b784-dc40ac75aa7d`: merged after one human-guided resume
- Final merge entries:
  - `6ae8f7a8-0d84-4395-90ed-a9ee1b97791d`
  - `c86bfc9b-b4ca-42cb-98ef-6fda5a5284b4`

Approximate wall-clock duration for the patched rerun from top-level execution start to final stream merge: `34m56s`.

## Captured Telemetry

Telemetry for both runs can now be generated directly from preserved daemon SQLite state.

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
- Started: `2026-04-12T16:12:10Z`
- Finished: `2026-04-12T17:07:31Z`
- Duration: `55m21s`
- Note: the benchmark run record itself was missing objective linkage, so the report inferred the objective from the single objective present in preserved `local12` daemon data

### Initial run stream telemetry

- `07eeca6f-2d96-4b14-8e52-7fe32e9355b6` `Command log navigation plumbing`: `1` execution, `0` review rejections, `0` human escalations, `1` merge attempt, duration `7m54s`
- `81a33862-69de-4f36-aa10-64ad911f601a` `Focused command log navigation coverage`: `2` executions, `7` review rejections, `4` automatic recovery decisions, `2` human escalations, `1` human-guided resume, `0` merge attempts, duration `41m27s`
- `1ab9f6a7-f4a5-465d-9f94-254b4393e13a` `Generated keybinding docs refresh`: `1` execution, `1` review rejection, `1` automatic recovery decision, `0` human escalations, `1` merge attempt, duration `8m11s`

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
- Started: `2026-04-12T17:24:50Z`
- Finished: `2026-04-12T17:59:46Z`
- Duration: `34m56s`
- Note: aggregate daemon run/objective rows were stale after merge; completion time is inferred from latest merged stream activity in preserved `local13` daemon data

### Patched rerun stream telemetry

- `4cc6948e-f84a-4a3b-9997-1ffe6b49b402` `Command log navigation bindings`: `1` execution, `0` review rejections, `0` human escalations, `1` merge attempt, duration `4m27s`
- `021a0e30-b3be-44a6-b784-dc40ac75aa7d` `Focused command log regression coverage`: `2` executions, `5` review rejections, `3` automatic recovery decisions, `1` human escalation, `1` human-guided resume, `1` merge attempt, duration `32m30s`

## Manual Score

The benchmark rubric is still manual. The telemetry counts above are now auto-generated from preserved daemon state.

### Initial run score: 5/10

- Behavioral correctness: `1/2`
  - stream 1 and docs stream merged, but the full feature benchmark did not complete
- Repo-native fit: `2/2`
  - merged changes fit the repo and reviewer accepted them where completed
- Scope discipline: `1/2`
  - streams were reasonable, but the coverage stream’s contract was too underspecified and the docs stream was broader than the feature core
- Planning / review quality: `0/2`
  - reviewer had to discover the true acceptance contract across many rounds
  - planner split implementation and proof without preserving the full contract for the proof stream
- Autonomy quality: `1/2`
  - autonomous loops worked, but the run still failed even after human guidance and required repeated rescue on the same stream

### Patched rerun score: 8/10

- Behavioral correctness: `2/2`
  - both streams merged, including the regression-coverage stream
- Repo-native fit: `2/2`
  - final changes were accepted by review and stayed within existing gui/integration conventions
- Scope discipline: `2/2`
  - the rerun reduced the benchmark to 2 tighter streams with clearer ownership
- Planning / review quality: `1/2`
  - materially improved: planner emitted explicit acceptance criteria up front
  - still not perfect: reviewer continued to refine the contract and eventually pointed stream 2 back toward possible product-behavior gaps, showing residual implementation/proof coupling
- Autonomy quality: `1/2`
  - one human-guided resume was still required for the regression-coverage stream

## Why The Rerun Scored Higher

The rerun improved in three clear ways:

1. **Fewer streams, clearer ownership**
- Initial run: 3 streams (behavior, coverage, docs)
- Rerun: 2 streams (behavior, regression coverage)

2. **Better contract fidelity up front**
- The rerun planner emitted explicit acceptance criteria, including visible behavior and focus scoping expectations for the coverage stream.

3. **Narrower review failures**
- Initial run reviewer feedback evolved through many stages: missing bindings, missing scoping, private internals, missing visible behavior, missing alternate keys.
- Rerun reviewer feedback was narrower and mostly centered on proving visible content and generated test registration.

## Remaining Weaknesses

The rerun still exposed two issues:

1. **Behavior/proof coupling remains leaky**
- The coverage stream was eventually told to add or verify product behavior in addition to proving it.
- That suggests the planner still split implementation and proof imperfectly for this slice.

2. **Resumed-run objective/run finalization bug**
- After the resumed stream merged, the stream state was correct, but the aggregate objective/run rows stayed stale in daemon state until patched in code.
- This report uses authoritative stream and merge data to assess the rerun outcome.

## Evidence Summary

### Initial run attempts

- Coverage stream review rejections: 7 recorded
- Coverage stream exhausted into `ask_human_then_resume` twice
- Docs stream review rejection: 1

### Patched rerun attempts

- Coverage stream review rejections recorded: 5
- Automatic recovery decisions before/around escalation: 3
- Human-guided resume: 1
- Final result: merged

### Patched rerun planner output quality

The rerun planner emitted explicit acceptance criteria such as:

- focused command log paging must visibly move the log viewport/content
- goto top/bottom must show earliest/newest content
- behavior must remain focus-scoped

That was not present in the same concrete form on the initial run.

## Conclusion

The patched rerun is a real improvement over the initial command-log benchmark run.

It did **not** make the system fully autonomous, but it did improve harness intelligence in a measurable way:

- the benchmark converged instead of stalling partial
- the planner front-loaded more of the real acceptance contract
- reviewer churn was narrower and less exploratory
- the final result merged successfully after one human-guided resume

This is evidence that strengthening planner/reviewer contract transfer improves benchmark performance, even before a full context dossier exists.
