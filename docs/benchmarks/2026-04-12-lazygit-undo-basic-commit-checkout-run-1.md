# Benchmark Report: lazygit.undo-basic-commit-checkout

**Date:** 2026-04-12  
**Benchmark run record:** `f5ed7a9e-8c7a-4caf-8c16-dbd59d747eda`  
**Daemon run:** `8ee114ae-9cf6-44fd-9903-8f0d86eaa910`  
**Objective:** `f40810f6-3e44-497b-9650-8b09b6d0dbbb`  
**Project:** `4b9ca013-cf24-446f-b29c-05b8f8fd5ae2`  
**Plan:** `98187854-c250-493d-a439-a94891339004`  
**Spec:** `lazygit.undo-basic-commit-checkout`  
**Repo baseline:** `43106b6c7fbe8c69cebb02f8fc80cb060faddeee`  
**Prompt:** `Add support for undoing recent plain commit and checkout actions using git reflog. Explicitly exclude pull --rebase, merge, revert, amend/reword, fixup/squash, and broader rebase flows from this benchmark slice.`

## Outcome

This run completed successfully end-to-end.

All planned streams merged:

- `3623dad6-f15c-49dc-8340-4c98f5ea03e5` - Narrow reflog undo core to plain commit and checkout
- `1ae11234-36b4-423a-adb0-76a4432da78c` - Exercise supported and unsupported undo flows in integration tests
- `ccbcf4a5-d87b-4843-985c-cafa5bfe0abd` - Update user-facing docs and copy for the benchmark slice

Final merge entries:

- `62665174-1d88-405c-8b0a-a05598aa20e8`
- `2bf6fd87-5882-456b-851a-9ea9c8c52ac9`
- `5134161a-2427-4a4f-938c-f4a543ac8509`

Top-level execution `91523b66-ade3-47b9-a5e9-93b10570da51` moved to `completed` at `1776007452`.

Approximate wall-clock duration from top-level execution start to completion: `39m26s`.

## Quality Gates Used

The successful run used the benchmark-spec quality gates, not planner-generated broad gates:

- `go test ./pkg/gui/controllers -count=1`
- `go test ./pkg/commands/git_commands -count=1`

This matters because earlier benchmark runs were invalidated by broad repo-wide gates that were red on the frozen baseline.

## Captured Telemetry

Telemetry for this run can now be generated directly from daemon SQLite via `tack benchmark report f5ed7a9e-8c7a-4caf-8c16-dbd59d747eda` against the preserved `local11` benchmark data.

- Streams: `3 total`, `3 merged`, `0 failed`, `0 non-terminal`
- Stream executions: `4`
- Builder sessions: `10`
- Reviewer sessions: `10`
- Recovery ledger entries: `8`
- Review rejections: `8`
- Automatic recovery decisions: `6`
- Human escalations: `1`
- Human resumes: `1`
- Human guidance provided: `1`
- Human guidance required: `yes`
- Merge attempts: `3`
- Started: `2026-04-12T14:44:46Z`
- Finished: `2026-04-12T15:24:12Z`
- Duration: `39m26s`

### Stream Telemetry

- `3623dad6-f15c-49dc-8340-4c98f5ea03e5` `Narrow reflog undo core to plain commit and checkout`: `2` executions, `5` review rejections, `3` automatic recovery decisions, `1` human escalation, `1` human-guided resume, `1` merge attempt, duration `21m2s`
- `1ae11234-36b4-423a-adb0-76a4432da78c` `Exercise supported and unsupported undo flows in integration tests`: `1` execution, `1` review rejection, `1` automatic recovery decision, `0` human escalations, `1` merge attempt, duration `11m17s`
- `ccbcf4a5-d87b-4843-985c-cafa5bfe0abd` `Update user-facing docs and copy for the benchmark slice`: `1` execution, `2` review rejections, `2` automatic recovery decisions, `0` human escalations, `1` merge attempt, duration `16m27s`

## Manual Score

This score is still manual. The telemetry counts above are now auto-generated from daemon state, but rubric scoring remains manual.

### 1. Behavioral correctness: 2/2

- The run completed successfully.
- All three streams merged.
- Review accepted the final implementation and benchmark-facing docs/copy.
- Stream 2 added and refined integration coverage for the supported and unsupported undo flows in the benchmark slice.

### 2. Repo-native fit: 2/2

- The work was accepted by the review step across controller, tests, docs, keybindings, and i18n/copy surfaces.
- Final changes stayed within existing lazygit patterns rather than introducing a benchmark-only structure.

### 3. Scope discipline: 1/2

- Stream 1 stayed tightly scoped.
- Stream 2 stayed mostly within expected integration-test surface.
- Stream 3 spread across docs, keybindings, GUI tooltip copy, and translations. Those edits were related, but broader than the controller/test core.

### 4. Planning / review quality: 2/2

- The 3-stream decomposition was effective.
- Review repeatedly caught real correctness issues:
  - unsupported reflog entries must act as barriers
  - initial-commit behavior was internally inconsistent
  - integration test organization needed to match the test runner's assumptions
  - docs/copy needed to match the actual supported benchmark slice
- Those review loops materially improved the result.

### 5. Autonomy quality: 1/2

- The run ultimately converged autonomously after recovery resumed.
- However, stream 1 exhausted the built-in review retry budget and required one human-guided resume.
- After that intervention, the run completed without further human rescue.

## Total Score

`8/10`

## Evidence Summary

- Stream 1 required `2` full stream executions before merge.
- Stream 1 accumulated `5` recorded review rejections, `3` automatic recovery decisions, `1` exhausted human escalation, and `1` guided resume.
- Streams 2 and 3 unblocked immediately after stream 1 merged and each completed in a single stream execution.
- Stream 2 accumulated `1` review rejection and `1` automatic recovery decision before merging.
- Stream 3 accumulated `2` review rejections and `2` automatic recovery decisions before merging.

### Key benchmark-system observations

This successful run happened only after fixing several benchmark/harness problems outside the target repository itself:

- merge completion now emits downstream-ready events so dependent streams actually start
- benchmark prepare now preflights benchmark gates against the frozen baseline
- benchmark execute now overrides planner-generated plan quality gates with benchmark-spec gates

Without those harness fixes, this run shape either stalled or failed for benchmark-only reasons.

## Important Caveat

The original write-up for this run was produced while benchmark run JSON sync was still buggy. The telemetry counts in this updated document come from the new benchmark report path over daemon SQLite state, which now preserves the full recovery history cleanly even when the older markdown narrative was approximate.

## Conclusion

This is the first successful end-to-end benchmark completion for the narrowed `lazygit.undo-basic-commit-checkout` slice.

The main result is not just that the repo changes merged. It is that Tack's benchmark execution path now behaved correctly under real conditions:

- benchmark baseline preparation was valid
- planner output was constrained to benchmark-safe gates
- dependent streams unblocked after merge
- parallel stream execution worked
- review/retry loops converged
- one human rescue was enough to finish the run

This run is a credible baseline for future comparison against context-engineering and dossier improvements.
