# Tack Phase 5: Merge & Review — PRD & Implementation Plan

Build the merge queue that processes completed stream branches into the main branch. This phase implements the merge queue store, merge processor with tiered conflict resolution, post-merge quality gate re-execution, diff extraction for client consumption, and the API/CLI surface for managing merges.

Phase 1 artifacts: `docs/phase1/PRD.md`, `docs/phase1/FINDINGS.md`
Phase 2 artifacts: `docs/phase2/PRD.md`, `docs/phase2/FINDINGS.md`
Phase 3 artifacts: `docs/phase3/PRD.md`, `docs/phase3/FINDINGS.md`
Phase 4 artifacts: `docs/phase4/PRD.md`, `docs/phase4/FINDINGS.md`

Atomic tasks for phased implementation. Each task targets 1-3 files max.
Track progress with checkboxes. Log decisions/findings in `FINDINGS.md`.

---

## Phase 1: Merge Queue Data Layer

The `merge_queue` table schema already exists in `internal/db/migrations.go`. This phase adds the domain model, database store, and extends stream status tracking for the merge lifecycle.

- [x] **1.1** Create merge queue domain model and store
  Add to `internal/domain/types.go`:

  ```go
  // MergeEntry represents a stream branch queued for merge.
  type MergeEntry struct {
      ID          string       `json:"id"`
      StreamID    string       `json:"stream_id"`
      PlanID      string       `json:"plan_id"`
      ObjectiveID string       `json:"objective_id"`
      Branch      string       `json:"branch"`
      Status      MergeStatus  `json:"status"`        // pending, merging, merged, failed, conflict
      Tier        int          `json:"tier"`           // 0 = not attempted, 1-4 = resolution tier used
      Error       string       `json:"error"`          // failure reason if any
      DiffStat    string       `json:"diff_stat"`      // JSON: {files_changed, insertions, deletions}
      CreatedAt   int64        `json:"created_at"`
      UpdatedAt   int64        `json:"updated_at"`
  }

  type MergeStatus string

  const (
      MergeStatusPending  MergeStatus = "pending"
      MergeStatusMerging  MergeStatus = "merging"
      MergeStatusMerged   MergeStatus = "merged"
      MergeStatusFailed   MergeStatus = "failed"
      MergeStatusConflict MergeStatus = "conflict"
  )
  ```

  Create `internal/db/merge_queue.go` with:

  ```go
  package db

  import (
      "context"
      "database/sql"
      "fmt"
      "time"
      "github.com/google/uuid"
      "github.com/syndg/tack/internal/domain"
  )

  type MergeQueueStore struct {
      db *sql.DB
  }

  func NewMergeQueueStore(db *sql.DB) *MergeQueueStore

  // Enqueue adds a stream branch to the merge queue.
  // Generates UUID, sets status to "pending", sets created_at/updated_at.
  func (s *MergeQueueStore) Enqueue(ctx context.Context, entry *domain.MergeEntry) error

  // Dequeue returns the next pending entry (FIFO by created_at).
  // Returns nil, nil if queue is empty.
  func (s *MergeQueueStore) Dequeue(ctx context.Context) (*domain.MergeEntry, error)

  // Get returns a merge entry by ID.
  func (s *MergeQueueStore) Get(ctx context.Context, id string) (*domain.MergeEntry, error)

  // GetByStream returns the merge entry for a stream (most recent).
  func (s *MergeQueueStore) GetByStream(ctx context.Context, streamID string) (*domain.MergeEntry, error)

  // UpdateStatus updates the status, tier, error, and diff_stat of a merge entry.
  func (s *MergeQueueStore) UpdateStatus(ctx context.Context, id string, status domain.MergeStatus, tier int, errMsg string, diffStat string) error

  // ListByObjective returns all merge entries for an objective's streams.
  func (s *MergeQueueStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.MergeEntry, error)

  // ListPending returns all pending entries ordered by created_at (FIFO).
  func (s *MergeQueueStore) ListPending(ctx context.Context) ([]domain.MergeEntry, error)

  // CountPending returns the number of pending entries.
  func (s *MergeQueueStore) CountPending(ctx context.Context) (int, error)
  ```

  The existing `merge_queue` table has columns: `id, stream_id, branch, status, created_at, updated_at`.
  Update the migration in `internal/db/migrations.go` to add the missing columns:
  - `plan_id TEXT NOT NULL DEFAULT ''`
  - `objective_id TEXT NOT NULL DEFAULT ''`
  - `tier INTEGER NOT NULL DEFAULT 0`
  - `error TEXT NOT NULL DEFAULT ''`
  - `diff_stat TEXT NOT NULL DEFAULT ''`

  Files: `internal/domain/types.go`, `internal/db/merge_queue.go`, `internal/db/migrations.go`

- [x] **1.2** Add stream merge status tracking
  Update `internal/domain/types.go` to add stream merge-related status constants:

  ```go
  const (
      StreamStatusMergeReady StreamStatus = "merge_ready"  // already exists
      StreamStatusMerging    StreamStatus = "merging"
      StreamStatusMerged     StreamStatus = "merged"
  )
  ```

  These are string constants used with `StreamStore.UpdateStatus()`. No schema change needed — the `status` column already accepts any string. The constants ensure consistent usage across the codebase.

  File: `internal/domain/types.go`

---

## Phase 2: Git Merge Operations

Implement the core git operations that perform branch merging with tiered conflict resolution. These operations run inside sandboxes via `Exec()`.

- [x] **2.1** Create merge operations package
  Create `internal/services/merge/git.go` with:

  ```go
  package merge

  import (
      "context"
      "fmt"
      "log/slog"
      "strings"
      "github.com/syndg/tack/internal/sandbox"
  )

  // MergeResult describes the outcome of a merge attempt.
  type MergeResult struct {
      Success      bool     `json:"success"`
      Tier         int      `json:"tier"`          // which tier resolved it (1-4)
      FilesChanged int      `json:"files_changed"`
      Insertions   int      `json:"insertions"`
      Deletions    int      `json:"deletions"`
      Conflicts    []string `json:"conflicts"`     // conflicting file paths (if any)
      Error        string   `json:"error"`
  }

  // GitMerger performs branch merge operations inside a sandbox.
  type GitMerger struct {
      logger *slog.Logger
  }

  func NewGitMerger(logger *slog.Logger) *GitMerger

  // Merge attempts to merge the given branch into the current branch (main).
  // Tries tiers sequentially: 1 (clean), 2 (auto-resolve), 3 (AI-resolve stub).
  // Returns the result with the tier that succeeded, or failure details.
  //
  // Flow:
  //   1. git fetch origin (ensure up-to-date)
  //   2. git merge --no-edit {branch} → if success, tier 1
  //   3. If conflicts: check if resolvable via auto-resolve (tier 2)
  //   4. If still conflicted: stub for tier 3 (AI merge) — returns conflict for now
  //   5. On any success: collect diff stats via git diff --stat
  func (m *GitMerger) Merge(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error)

  // TryCleanMerge attempts a tier 1 clean merge (no conflicts).
  // Runs: git merge --no-edit {branch}
  // Returns success=true if merge completes without conflicts.
  // On conflict: runs git merge --abort and returns success=false with conflict list.
  func (m *GitMerger) TryCleanMerge(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error)

  // TryAutoResolve attempts tier 2 auto-resolution.
  // For each conflicting file:
  //   1. Check if conflicts are in non-overlapping sections (textual, not semantic)
  //   2. If all conflicts are textual: git checkout --theirs for non-overlapping, manual resolve for rest
  //   3. For now, simplified: use git merge -X theirs to favor incoming changes
  // This is appropriate when streams have file scope isolation (no overlapping edits).
  func (m *GitMerger) TryAutoResolve(ctx context.Context, sb sandbox.Sandbox, branch string) (*MergeResult, error)

  // GetDiffStat returns diff statistics for the last merge.
  // Runs: git diff --stat HEAD~1
  // Parses output to extract files changed, insertions, deletions.
  func (m *GitMerger) GetDiffStat(ctx context.Context, sb sandbox.Sandbox) (filesChanged, insertions, deletions int, err error)

  // GetConflictFiles returns the list of files with merge conflicts.
  // Runs: git diff --name-only --diff-filter=U
  func (m *GitMerger) GetConflictFiles(ctx context.Context, sb sandbox.Sandbox) ([]string, error)

  // AbortMerge runs git merge --abort to clean up after a failed merge attempt.
  func (m *GitMerger) AbortMerge(ctx context.Context, sb sandbox.Sandbox) error
  ```

  **Tier 1 (clean merge) implementation:**
  1. Run `git merge --no-edit {branch}` via `sb.Exec()`
  2. If exit code 0: success, collect diff stats, return tier=1
  3. If exit code != 0 and stderr contains "CONFLICT": get conflict files, abort merge, return success=false

  **Tier 2 (auto-resolve) implementation:**
  1. Run `git merge -X theirs --no-edit {branch}` via `sb.Exec()`
  2. This strategy favors incoming changes — appropriate when file scope isolation prevents true semantic conflicts
  3. If exit code 0: success, collect diff stats, return tier=2
  4. If still fails: abort merge, return success=false

  **Tier 3 (AI merge):**
  Stub for now — log that tier 3 is not yet implemented, return success=false.
  Phase 8 (Polish & Ecosystem) will implement AI-assisted conflict resolution via a merger agent.

  File: `internal/services/merge/git.go`

- [x] **2.2** Create diff extraction
  Create `internal/services/merge/diff.go` with:

  ```go
  package merge

  import (
      "context"
      "encoding/json"
      "fmt"
      "log/slog"
      "strings"
      "github.com/syndg/tack/internal/sandbox"
  )

  // FileDiff represents the diff for a single file.
  type FileDiff struct {
      Path       string `json:"path"`
      Status     string `json:"status"`     // added, modified, deleted, renamed
      Insertions int    `json:"insertions"`
      Deletions  int    `json:"deletions"`
      Patch      string `json:"patch"`      // unified diff content
  }

  // DiffSummary is the aggregate diff data for a merge.
  type DiffSummary struct {
      FilesChanged int        `json:"files_changed"`
      Insertions   int        `json:"insertions"`
      Deletions    int        `json:"deletions"`
      Files        []FileDiff `json:"files"`
  }

  // DiffExtractor generates diff data for client consumption.
  type DiffExtractor struct {
      logger *slog.Logger
  }

  func NewDiffExtractor(logger *slog.Logger) *DiffExtractor

  // Extract generates a DiffSummary between two refs (e.g., "main" and "stream-branch").
  // Runs: git diff --stat {base}...{head}
  // Runs: git diff --name-status {base}...{head}
  // Runs: git diff {base}...{head} (for full patch)
  // Parses output into structured DiffSummary.
  func (d *DiffExtractor) Extract(ctx context.Context, sb sandbox.Sandbox, base, head string) (*DiffSummary, error)

  // ExtractJSON returns the DiffSummary as a JSON string for storage in diff_stat.
  func (d *DiffExtractor) ExtractJSON(ctx context.Context, sb sandbox.Sandbox, base, head string) (string, error)

  // ParseDiffStat parses the output of `git diff --stat` into structured data.
  // Example line: " src/auth/token.ts | 42 +++----"
  // Returns per-file insertions/deletions and totals.
  func ParseDiffStat(output string) (files []FileDiff, totalInsertions, totalDeletions int)

  // ParseNameStatus parses `git diff --name-status` output.
  // Example: "M\tsrc/auth/token.ts" or "A\tsrc/auth/jwt.ts"
  func ParseNameStatus(output string) map[string]string
  ```

  File: `internal/services/merge/diff.go`

---

## Phase 3: Merge Queue Processor

The central merge processing loop. Consumes the merge queue FIFO, coordinates git merges, runs post-merge quality gates, handles failures, and publishes events.

- [x] **3.1** Create merge queue processor
  Create `internal/services/merge/processor.go` with:

  ```go
  package merge

  import (
      "context"
      "encoding/json"
      "fmt"
      "log/slog"
      "sync"
      "time"
      "github.com/syndg/tack/internal/db"
      "github.com/syndg/tack/internal/domain"
      "github.com/syndg/tack/internal/harness/gates"
      "github.com/syndg/tack/internal/sandbox"
      events "github.com/syndg/tack/internal/services/events"
  )

  // Processor manages the merge queue lifecycle.
  type Processor struct {
      queue      *db.MergeQueueStore
      streams    *db.StreamStore
      plans      *db.PlanStore
      objectives *db.ObjectiveStore
      merger     *GitMerger
      differ     *DiffExtractor
      gateRunner *gates.Runner
      sandboxProv sandbox.SandboxProvider
      eventBus   *events.PersistentBus
      logger     *slog.Logger

      mu         sync.Mutex
      processing bool
      cancel     context.CancelFunc
  }

  func NewProcessor(
      queue *db.MergeQueueStore,
      streams *db.StreamStore,
      plans *db.PlanStore,
      objectives *db.ObjectiveStore,
      merger *GitMerger,
      differ *DiffExtractor,
      gateRunner *gates.Runner,
      sandboxProv sandbox.SandboxProvider,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Processor

  // Start begins the merge processor loop.
  // Subscribes to EventMergeQueued to trigger processing.
  // Also runs on a ticker (every 30s) to catch any missed events.
  func (p *Processor) Start(ctx context.Context) error

  // Stop cancels the processor loop.
  func (p *Processor) Stop()

  // ProcessNext dequeues and processes the next pending merge entry.
  // Returns true if an entry was processed, false if queue was empty.
  //
  // Flow:
  //   1. Dequeue next pending entry
  //   2. Check dependency ordering: if stream has dependencies,
  //      verify all dependency streams are already merged
  //   3. Update entry status to "merging"
  //   4. Get or create a sandbox for merge operations
  //   5. Attempt merge via GitMerger.Merge()
  //   6. If merge succeeds:
  //      a. Run post-merge quality gates in the sandbox
  //      b. If gates pass: extract diff, update entry to "merged", update stream to "merged"
  //      c. If gates fail: revert merge (git reset --hard HEAD~1), mark entry "failed"
  //   7. If merge fails (conflict):
  //      a. If tier < 3: retry at next tier (re-enqueue with higher tier start)
  //      b. If tier 3+ fails: mark "conflict", publish EventMergeFailed for escalation
  //   8. Publish EventMergeCompleted or EventMergeFailed
  //   9. Check if all streams for the objective are merged → transition to reviewing
  func (p *Processor) ProcessNext(ctx context.Context) (bool, error)

  // EnqueueStream creates a merge entry for a stream that has signaled merge_ready.
  // Called by the merge_queue deterministic handler.
  //   1. Look up the stream to get branch name and plan_id
  //   2. Look up the plan to get objective_id
  //   3. Create MergeEntry with status "pending"
  //   4. Enqueue via store
  //   5. Publish EventMergeQueued
  func (p *Processor) EnqueueStream(ctx context.Context, streamID string) error

  // checkDependencies verifies that all dependency streams for this stream
  // have already been merged. Returns true if all deps are satisfied.
  func (p *Processor) checkDependencies(ctx context.Context, entry *domain.MergeEntry) (bool, error)

  // runPostMergeGates runs quality gates after a successful merge.
  // Uses the plan's quality gates configuration.
  // Returns the gate results.
  func (p *Processor) runPostMergeGates(ctx context.Context, sb sandbox.Sandbox, planID string) (*gates.RunResult, error)

  // checkObjectiveComplete checks if all streams for an objective have been merged.
  // If so, transitions the objective to "reviewing" status.
  func (p *Processor) checkObjectiveComplete(ctx context.Context, objectiveID string) error
  ```

  **ProcessNext detailed flow:**
  1. Call `queue.Dequeue()` — returns nil if empty
  2. Call `checkDependencies()` — if not satisfied, skip entry (leave pending, process next)
  3. Update entry: `status = "merging"`, `updated_at = now`
  4. Update stream: `status = "merging"`
  5. Get sandbox for merge work:
     - Use `sandboxProv.Get()` to find an existing sandbox for the objective
     - Or create a temporary one via `sandboxProv.Create()` with labels `{tack.role: merger, tack.objective: objectiveID}`
  6. Call `merger.Merge(ctx, sb, entry.Branch)`:
     - On success: `runPostMergeGates()`, then:
       - Gates pass → extract diff, store in entry, update entry `status = "merged"`, update stream `status = "merged"`, publish `EventMergeCompleted`
       - Gates fail → revert merge in sandbox, update entry `status = "failed"` with gate error, publish `EventMergeFailed`
     - On conflict (all tiers exhausted) → update entry `status = "conflict"`, publish `EventMergeFailed` with escalation payload
  7. Call `checkObjectiveComplete()` to see if all streams are done

  **Dependency ordering:**
  The plan's `Stream.Dependencies` field lists stream IDs that must merge before this stream.
  `checkDependencies()` queries the merge queue store for each dependency stream and verifies
  its entry has `status = "merged"`. If any dependency is not merged, the entry stays pending.

  File: `internal/services/merge/processor.go`

---

## Phase 4: Wiring & Handlers

Connect the merge processor to the existing blueprint engine and daemon infrastructure.

- [x] **4.1** Wire merge processor into daemon
  Update `internal/daemon/daemon.go` to:

  1. Add fields to `Daemon` struct:
  ```go
  mergeQueueStore *db.MergeQueueStore
  mergeProcessor  *merge.Processor
  ```

  2. Add import:
  ```go
  "github.com/syndg/tack/internal/services/merge"
  ```

  3. In `New()`:
  ```go
  // Create merge queue store
  mergeQueueStore := db.NewMergeQueueStore(database)

  // Create merge processor components
  gitMerger := merge.NewGitMerger(logger)
  diffExtractor := merge.NewDiffExtractor(logger)

  // Create merge processor
  mergeProcessor := merge.NewProcessor(
      mergeQueueStore, streamStore, planStore, objectiveStore,
      gitMerger, diffExtractor, gateRunner, sandboxProv,
      eventBus, logger,
  )
  ```

  4. In `Start()`: call `mergeProcessor.Start(ctx)`
  5. In `Stop()`: call `mergeProcessor.Stop()`

  6. Pass `mergeProcessor` to `dispatch.NewHandlers()` so the `merge_queue` deterministic action
     can call `mergeProcessor.EnqueueStream()` instead of being a stub.

  File: `internal/daemon/daemon.go`

- [x] **4.2** Replace merge_queue stub in handlers
  Update `internal/services/dispatch/handlers.go`:

  1. Add `mergeProcessor *merge.Processor` field to `Handlers` struct
  2. Update `NewHandlers()` to accept `mergeProcessor` parameter
  3. Replace the `mergeQueue` stub:

  ```go
  func (h *Handlers) mergeQueue(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
      // Get the plan for this objective
      plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
      if err != nil {
          return blueprint.StepResult{Status: blueprint.StepStatusFailed}, fmt.Errorf("getting plan: %w", err)
      }

      // Get all streams for the plan
      streams, err := h.streams.ListByPlan(ctx, plan.ID)
      if err != nil {
          return blueprint.StepResult{Status: blueprint.StepStatusFailed}, fmt.Errorf("listing streams: %w", err)
      }

      // Enqueue all merge_ready streams
      for _, stream := range streams {
          if stream.Status == "merge_ready" {
              if err := h.mergeProcessor.EnqueueStream(ctx, stream.ID); err != nil {
                  h.logger.Error("failed to enqueue stream for merge", "stream", stream.ID, "error", err)
              }
          }
      }

      // The merge processor runs asynchronously.
      // The blueprint advances — the processor will transition the objective
      // to "reviewing" when all merges complete.
      return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
  }
  ```

  File: `internal/services/dispatch/handlers.go`

---

## Phase 5: API & CLI

Expose merge queue management and diff viewing through the HTTP API and CLI.

- [x] **5.1** Add merge queue HTTP routes
  Add to `internal/daemon/routes.go`:

  ```go
  // GET /merge-queue — list all merge queue entries (optionally filter by objective)
  // Query param: ?objective={id}
  // Returns JSON array of MergeEntry.
  func (d *Daemon) handleListMergeQueue(w http.ResponseWriter, r *http.Request)

  // GET /merge-queue/{id} — get a single merge entry
  func (d *Daemon) handleGetMergeEntry(w http.ResponseWriter, r *http.Request)

  // POST /merge-queue/{id}/retry — retry a failed or conflicted merge entry
  // Resets status to "pending" and re-enqueues.
  func (d *Daemon) handleRetryMerge(w http.ResponseWriter, r *http.Request)

  // GET /streams/{id}/diff — get the diff summary for a merged stream
  // Returns the DiffSummary stored in the merge entry's diff_stat field.
  func (d *Daemon) handleGetStreamDiff(w http.ResponseWriter, r *http.Request)
  ```

  Register routes in `registerRoutes()`:
  ```go
  d.mux.HandleFunc("GET /merge-queue", d.handleListMergeQueue)
  d.mux.HandleFunc("GET /merge-queue/{id}", d.handleGetMergeEntry)
  d.mux.HandleFunc("POST /merge-queue/{id}/retry", d.handleRetryMerge)
  d.mux.HandleFunc("GET /streams/{id}/diff", d.handleGetStreamDiff)
  ```

  `handleListMergeQueue`: reads `?objective=` query param. If present, calls
  `mergeQueueStore.ListByObjective()`. Otherwise, calls `mergeQueueStore.ListPending()`.

  `handleRetryMerge`: looks up the entry, verifies status is "failed" or "conflict",
  resets status to "pending" via `mergeQueueStore.UpdateStatus()`.

  `handleGetStreamDiff`: looks up the merge entry for the stream via
  `mergeQueueStore.GetByStream()`, returns the parsed `diff_stat` JSON.

  File: `internal/daemon/routes.go`

- [x] **5.2** Add merge queue client methods
  Add to `internal/client/client.go`:

  ```go
  // ListMergeQueue returns merge queue entries, optionally filtered by objective.
  func (c *Client) ListMergeQueue(ctx context.Context, objectiveID string) ([]domain.MergeEntry, error)

  // GetMergeEntry returns a single merge queue entry.
  func (c *Client) GetMergeEntry(ctx context.Context, id string) (*domain.MergeEntry, error)

  // RetryMerge resets a failed merge entry to pending.
  func (c *Client) RetryMerge(ctx context.Context, id string) error

  // GetStreamDiff returns the diff summary for a merged stream.
  func (c *Client) GetStreamDiff(ctx context.Context, streamID string) (*merge.DiffSummary, error)
  ```

  All methods follow the existing `do()` helper pattern.

  File: `internal/client/client.go`

- [x] **5.3** Create `tack merge` command
  Create `cmd/tack/merge.go` with:

  ```go
  var mergeCmd = &cobra.Command{
      Use:   "merge",
      Short: "View merge queue status",
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          entries, err := c.ListMergeQueue(cmd.Context(), mergeObjective)
          // Print table: ID | STREAM | BRANCH | STATUS | TIER | CREATED
      },
  }

  var mergeRetryCmd = &cobra.Command{
      Use:   "retry [entry-id]",
      Short: "Retry a failed merge",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          err := c.RetryMerge(cmd.Context(), args[0])
          if err != nil {
              return err
          }
          fmt.Printf("Merge entry %s re-queued.\n", args[0])
          return nil
      },
  }

  var mergeDiffCmd = &cobra.Command{
      Use:   "diff [stream-id]",
      Short: "View diff for a merged stream",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          diff, err := c.GetStreamDiff(cmd.Context(), args[0])
          // Print: files changed, insertions, deletions
          // For each file: path, status, +/- counts
      },
  }
  ```

  Format `mergeCmd` output as aligned table using `text/tabwriter`:
  ```
  ID        STREAM    BRANCH                          STATUS    TIER  CREATED
  a1b2c3    d4e5f6    tack/d4e5f6/builder-g7h8i9      merged    1     5m ago
  j0k1l2    m3n4o5    tack/m3n4o5/builder-p6q7r8      pending   0     2m ago
  ```

  `mergeDiffCmd` output:
  ```
  Diff for stream d4e5f6:
    3 files changed, 42 insertions(+), 8 deletions(-)

    M  src/auth/token.ts      (+28, -4)
    A  src/auth/jwt.ts         (+14, -0)
    M  src/auth/token.test.ts  (+0, -4)
  ```

  Register with `rootCmd`:
  ```go
  var mergeObjective string

  func init() {
      mergeCmd.Flags().StringVar(&mergeObjective, "objective", "", "filter by objective ID")
      mergeCmd.AddCommand(mergeRetryCmd)
      mergeCmd.AddCommand(mergeDiffCmd)
      rootCmd.AddCommand(mergeCmd)
  }
  ```

  Reuse `truncateID` and `timeAgo` helpers from `plans.go`.

  File: `cmd/tack/merge.go`

---

## Phase 6: Tests

- [x] **6.1** Add unit tests
  Create test files:

  `internal/db/merge_queue_test.go`:
  - Test `Enqueue` persists entry with correct fields and generated UUID
  - Test `Dequeue` returns oldest pending entry (FIFO order)
  - Test `Dequeue` returns nil when queue is empty
  - Test `Dequeue` skips non-pending entries
  - Test `GetByStream` returns the most recent entry for a stream
  - Test `UpdateStatus` updates status, tier, error, diff_stat, and updated_at
  - Test `ListByObjective` returns only entries matching the objective
  - Test `ListPending` returns entries ordered by created_at
  - Test `CountPending` returns correct count

  `internal/services/merge/git_test.go`:
  - Test `TryCleanMerge` success path (exit code 0 → tier 1 success)
  - Test `TryCleanMerge` conflict path (exit code != 0, stderr has CONFLICT → abort + return conflicts)
  - Test `TryAutoResolve` success path (exit code 0 → tier 2 success)
  - Test `TryAutoResolve` failure path (still conflicts after -X theirs)
  - Test `GetDiffStat` parses stat output correctly
  - Test `GetConflictFiles` parses name-only output
  - Test `AbortMerge` calls git merge --abort
  - Test `Merge` tries tiers sequentially (tier 1 fail → tier 2 attempt)
  - Use mock sandbox that returns configured ExecResults per command

  `internal/services/merge/diff_test.go`:
  - Test `ParseDiffStat` with various git output formats
  - Test `ParseNameStatus` with A/M/D/R status codes
  - Test `Extract` with mock sandbox returning diff output
  - Test `ExtractJSON` returns valid JSON

  `internal/services/merge/processor_test.go`:
  - Test `EnqueueStream` creates merge entry and publishes event
  - Test `ProcessNext` returns false for empty queue
  - Test `ProcessNext` skips entry with unsatisfied dependencies
  - Test `ProcessNext` successful merge (tier 1) → merged status + gates pass
  - Test `ProcessNext` failed gates → revert + failed status
  - Test `checkObjectiveComplete` transitions objective when all streams merged
  - Test `checkDependencies` with satisfied and unsatisfied deps

  Files:
  - `internal/db/merge_queue_test.go`
  - `internal/services/merge/git_test.go`
  - `internal/services/merge/diff_test.go`
  - `internal/services/merge/processor_test.go`

---

## Execution Order

| Step | Task | Phase |
|------|------|-------|
| 1 | 1.1 Create merge queue domain model and store | 1 |
| 2 | 1.2 Add stream merge status tracking | 1 |
| 3 | 2.1 Create merge operations package | 2 |
| 4 | 2.2 Create diff extraction | 2 |
| 5 | 3.1 Create merge queue processor | 3 |
| 6 | 4.1 Wire merge processor into daemon | 4 |
| 7 | 4.2 Replace merge_queue stub in handlers | 4 |
| 8 | 5.1 Add merge queue HTTP routes | 5 |
| 9 | 5.2 Add merge queue client methods | 5 |
| 10 | 5.3 Create tack merge command | 5 |
| 11 | 6.1 Add unit tests | 6 |
