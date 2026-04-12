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

## Manual Score

This score is manual. Tack does not yet have an automated rubric scorer or final benchmark report generator.

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

### Stream progression

- Stream 1 initially failed review several times, then hit `ask_human_then_resume`.
- A guided retry resumed at `1776006220`.
- Stream 1 merged at `1776006464`.
- Streams 2 and 3 unblocked immediately and ran in parallel.
- Stream 2 merged at `1776007143`.
- Stream 3 merged at `1776007452`.

### Recovery / intervention count

Recorded recovery attempts for this objective:

- Stream 1:
  - 2 review rejections with automatic builder reruns
  - 1 exhausted review rejection leading to human guidance
  - 1 guided resume record
  - 1 additional review rejection after resume, then successful completion
- Stream 2:
  - 1 review rejection with automatic builder rerun
- Stream 3:
  - 2 review rejections with automatic builder reruns

### Key benchmark-system observations

This successful run happened only after fixing several benchmark/harness problems outside the target repository itself:

- merge completion now emits downstream-ready events so dependent streams actually start
- benchmark prepare now preflights benchmark gates against the frozen baseline
- benchmark execute now overrides planner-generated plan quality gates with benchmark-spec gates

Without those harness fixes, this run shape either stalled or failed for benchmark-only reasons.

## Important Caveat

The persisted benchmark run record in `benchmarks/runs.json` was still stale at the time of reporting and did not reflect the completed daemon-side outcome. The authoritative result for this report comes from the daemon SQLite state and final event stream, not the stale benchmark run JSON.

That mismatch should be fixed in the benchmark record sync path before relying on `benchmark show-run` as the final source of truth.

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
