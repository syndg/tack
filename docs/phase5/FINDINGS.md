# Phase 5 Findings

## Task 1.1: Create merge queue domain model and store

- Changed `merge_queue` table PK from `INTEGER PRIMARY KEY AUTOINCREMENT` to `TEXT PRIMARY KEY` to use UUIDs, consistent with all other stores (objectives, plans, streams, agents, executions).
- Added 5 new columns to migration: `plan_id`, `objective_id`, `tier`, `error`, `diff_stat`. Also added `ensureColumnExists` calls in `RunMigrations` for backward compatibility with existing DBs.
- `MergeEntry.CreatedAt`/`UpdatedAt` use `int64` (Unix timestamps) instead of `time.Time` — matches the DB storage format directly and avoids the conversion dance other stores do. Future tasks should keep this in mind when using MergeEntry.
- Added `scannable` interface in `merge_queue.go` to share scanning logic between `*sql.Row` and `*sql.Rows` — avoids duplicating the 11-column scan.
- `Dequeue` does not mark the entry as "merging" — it only reads. The caller (processor in task 3.1) is responsible for status transitions, keeping the store layer free of business logic.
- All 8 store methods implemented: `Enqueue`, `Dequeue`, `Get`, `GetByStream`, `UpdateStatus`, `ListByObjective`, `ListPending`, `CountPending`.
- Full `go build ./...` passes with no errors.

## Task 1.2: Add stream merge status tracking

- Added `StreamStatus` as a type alias (`type StreamStatus = string`) rather than a distinct type, so it's assignment-compatible with the existing `Stream.Status string` field and `StreamStore.UpdateStatus(ctx, id, string)` signature — no refactoring needed.
- Added three constants: `StreamStatusMergeReady` ("merge_ready"), `StreamStatusMerging` ("merging"), `StreamStatusMerged` ("merged").
- Did not change the `Stream` struct's `Status` field type from `string` to `StreamStatus` — that would ripple across many files and isn't required by this task.
- Existing code already uses `"merge_ready"` as a string literal in `handlers.go:270`. Future tasks can replace those literals with the constant.
- `go build ./...` passes with no errors.

## Phase 1 — Build Fix (attempt 1)

- **Problem**: `ralph.sh` line 18 set `PROJECT_DIR` by navigating two levels up from the script's directory (`$(dirname "$0")/../..`). The script lives at the repo root, so this resolved to `/Volumes/External` instead of `/Volumes/External/Coding/deck`. The `go build ./...` command then ran from a directory with no `go.mod`, producing: `pattern ./...: directory prefix . does not contain main module or its selected dependencies`.
- **Fix**: Changed `PROJECT_DIR` to use `$(dirname "$0")` directly (no `/../..`), since the script is at the project root.
- `go build ./...` and `go vet ./...` both pass.

## Phase 1 — Build Fix (attempt 2)

- **Problem**: Same error as attempt 1 — `pattern ./...: directory prefix . does not contain main module or its selected dependencies`. Attempt 1 correctly fixed the `ralph.sh` *file* (changed `PROJECT_DIR` from `$(dirname "$0")/../..` to `$(dirname "$0")`), but the **running** ralph.sh process still holds the stale `PROJECT_DIR=/Volumes/External` variable. Shell variables set at script startup aren't refreshed when the file is edited on disk.
- **Go code is fine**: `go build ./...` and `go vet ./...` both pass from `/Volumes/External/Coding/deck`. The merge queue store, domain types, and migrations all compile correctly.
- **Resolution**: No Go code changes needed. The ralph.sh file fix from attempt 1 is correct but requires a **restart** of ralph.sh to take effect. The running process will continue to fail on sanity checks until restarted.

## Phase 1 — Build Fix (attempt 3)

- **Same root cause as attempts 1 & 2**: The `go build ./...` error (`directory prefix . does not contain main module`) is caused by the running ralph.sh process holding a stale `PROJECT_DIR=/Volumes/External` variable. The fix on disk (line 18: `PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"`) is already correct since attempt 1.
- **No Go code changes needed**: `go build ./...` and `go vet ./...` both pass cleanly from `/Volumes/External/Coding/deck`. All Phase 1 files (`internal/db/merge_queue.go`, `internal/db/migrations.go`, `internal/domain/types.go`) compile without errors.
- **Action required**: Ralph must be **restarted** so the corrected `PROJECT_DIR` takes effect. The running process cannot self-heal this variable — it was set once at startup and the in-memory value persists regardless of file edits.

## Task 2.1: Create merge operations package

- Created `internal/services/merge/git.go` — the `GitMerger` type with full implementations of `Merge`, `TryCleanMerge`, `TryAutoResolve`, `GetDiffStat`, `GetConflictFiles`, and `AbortMerge`.
- `Merge()` tries tiers sequentially: tier 1 (clean `git merge --no-edit`), tier 2 (`git merge -X theirs --no-edit`), tier 3 (stub — logs warning, returns failure). Tier 3 AI merge is deferred to Phase 8.
- `TryCleanMerge` fetches origin first (`git fetch origin`) to ensure refs are up-to-date before attempting the merge. `TryAutoResolve` does not re-fetch since it runs immediately after a tier 1 failure.
- Conflict detection checks both `res.Stderr` and `res.Stdout` for "CONFLICT" — git versions vary on where they output the conflict message.
- `GetDiffStat` parses the summary line of `git diff --stat HEAD~1` using a regex that handles optional insertions/deletions (e.g., a file-only-deleted diff won't have an insertions clause).
- Exported `parseDiffStatSummary` as unexported helper — kept internal since task 2.2 (`diff.go`) will have its own `ParseDiffStat` for different purposes.
- All sandbox operations use `sandbox.ExecOpts{}` (zero value) — the caller (processor, task 3.1) is responsible for setting work dir and timeouts on the sandbox itself.
- `go build ./...` and `go vet ./...` pass with no errors.

## Task 2.2: Create diff extraction

- Created `internal/services/merge/diff.go` with `DiffExtractor`, `DiffSummary`, `FileDiff` types and all specified methods.
- `Extract` runs three git diff commands via `sb.Exec()`: `--stat` (per-file +/- counts), `--name-status` (A/M/D/R labels), and full diff (patch content). Results are merged by file path into `[]FileDiff`.
- `ParseDiffStat` handles the `git diff --stat` format including binary files (`Bin 0 -> N bytes`), the +/- indicator characters, and the summary line (`N files changed, N insertions(+), N deletions(-)`). Uses `extractNumberBefore` helper to find the number preceding "insertion"/"deletion" keywords.
- `ParseNameStatus` handles rename entries (`R100\told\tnew`) by using the destination path, and maps single-char status codes to human labels via `nameStatusToLabel`.
- `parsePatchOutput` (unexported) splits unified diff output on `"diff --git "` boundaries and keys by the `b/` path — handles renames correctly.
- Added `strconv` import (beyond PRD's listed imports) for parsing insertion/deletion counts from the stat summary line.
- `go build ./...` passes with no errors.
