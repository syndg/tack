# Deck Phase 2: Harness Core — PRD & Implementation Plan

Build the deterministic harness infrastructure that constrains and informs agents. This phase produces the blueprint engine (YAML state machine), scoped rules engine (glob-matched context injection), tool curator (per-agent tool selection), and quality gate runner (deterministic validation in sandboxes).

Phase 1 artifacts: `docs/phase1/PRD.md`, `docs/phase1/FINDINGS.md`

Atomic tasks for phased implementation. Each task targets 1-3 files max.
Track progress with checkboxes. Log decisions/findings in `FINDINGS.md`.

---

## Phase 1: Blueprint Types & Loader

- [x] **1.1** Create blueprint domain types
  Create `internal/harness/blueprint/types.go` with all blueprint-related types.

  ```go
  package blueprint

  type StepType string
  const (
      StepTypeAgent         StepType = "agent"
      StepTypeDeterministic StepType = "deterministic"
      StepTypeHuman         StepType = "human"
      StepTypeBlueprintRef  StepType = "blueprint_ref"
  )

  type Blueprint struct {
      Name        string `yaml:"name"`
      Description string `yaml:"description"`
      Trigger     string `yaml:"trigger"`
      Steps       []Step `yaml:"steps"`
  }

  type Step struct {
      ID          string   `yaml:"id"`
      Type        StepType `yaml:"type"`
      Role        string   `yaml:"role,omitempty"`
      Action      string   `yaml:"action,omitempty"`
      Ref         string   `yaml:"ref,omitempty"`
      Description string   `yaml:"description,omitempty"`
      Next        string   `yaml:"next,omitempty"`
      Retry       int      `yaml:"retry,omitempty"`
      Optional    bool     `yaml:"optional,omitempty"`
      Tools       *ToolScope `yaml:"tools,omitempty"`
  }

  type ToolScope struct {
      Include []string `yaml:"include,omitempty"`
      Exclude []string `yaml:"exclude,omitempty"`
  }

  type StepStatus string
  const (
      StepStatusPending   StepStatus = "pending"
      StepStatusRunning   StepStatus = "running"
      StepStatusCompleted StepStatus = "completed"
      StepStatusFailed    StepStatus = "failed"
      StepStatusSkipped   StepStatus = "skipped"
      StepStatusBlocked   StepStatus = "blocked"
  )

  // StepState tracks runtime state for a step within an execution.
  type StepState struct {
      StepID     string     `json:"step_id"`
      Status     StepStatus `json:"status"`
      RetryCount int        `json:"retry_count"`
      Error      string     `json:"error,omitempty"`
  }
  ```

  All types need both `yaml` tags (for loading blueprints) and `json` tags (for API/persistence where relevant).
  File: `internal/harness/blueprint/types.go`

- [x] **1.2** Create blueprint YAML loader
  Create `internal/harness/blueprint/loader.go` with:

  ```go
  package blueprint

  // LoadFile loads a single blueprint from a YAML file.
  func LoadFile(path string) (*Blueprint, error)

  // LoadDir loads all blueprints from a directory (non-recursive).
  // Returns a map keyed by blueprint name.
  func LoadDir(dir string) (map[string]*Blueprint, error)

  // Validate checks a blueprint for structural correctness:
  // - All steps have a unique ID
  // - All "next" references point to existing step IDs
  // - Agent steps have a role
  // - Deterministic steps have an action
  // - BlueprintRef steps have a ref
  // - No unreachable steps (except the first step, which is the entry point)
  func Validate(bp *Blueprint) error
  ```

  `LoadFile`: read YAML via `os.ReadFile` + `yaml.Unmarshal`. Call `Validate` after loading.
  `LoadDir`: read directory via `os.ReadDir`, load all `.yaml`/`.yml` files, skip non-blueprint files gracefully.
  `Validate`: return a `fmt.Errorf` with all validation errors joined (not just the first).

  Import: `os`, `path/filepath`, `fmt`, `strings`, `gopkg.in/yaml.v3`.
  File: `internal/harness/blueprint/loader.go`

- [x] **1.3** Create shipped default blueprints
  Create three default blueprint YAML files matching the design doc:

  `internal/harness/blueprint/defaults/feature.yaml`:
  ```yaml
  name: "Feature Implementation"
  description: "Plan, build, review, and merge a new feature"
  trigger: "default"

  steps:
    - id: plan
      type: agent
      role: planner
      description: "Explore codebase and decompose into streams"
      next: approve

    - id: approve
      type: human
      description: "Review and approve the plan"
      next: dispatch

    - id: dispatch
      type: deterministic
      action: dispatch_streams
      description: "Spawn sandboxes and agents per stream"
      next: per_stream

    - id: per_stream
      type: blueprint_ref
      ref: ".deck/blueprints/stream.yaml"
      next: merge

    - id: merge
      type: deterministic
      action: merge_queue
      description: "Merge all stream branches, run quality gates"
      next: complete

    - id: complete
      type: deterministic
      action: mark_complete
  ```

  `internal/harness/blueprint/defaults/stream.yaml`:
  ```yaml
  name: "Stream Execution"
  description: "Execute a single work stream: build, lint, review"

  steps:
    - id: build
      type: agent
      role: builder
      description: "Implement the stream's task"
      next: lint

    - id: lint
      type: deterministic
      action: run_quality_gates
      retry: 2
      next: review

    - id: review
      type: agent
      role: reviewer
      description: "Review the implementation"
      next: merge_ready

    - id: merge_ready
      type: deterministic
      action: signal_merge_ready
  ```

  `internal/harness/blueprint/defaults/hotfix.yaml`:
  ```yaml
  name: "Hotfix"
  description: "Single-agent fix with minimal ceremony"
  trigger: "manual"

  steps:
    - id: fix
      type: agent
      role: builder
      description: "Fix the issue in a single agent session"
      next: lint

    - id: lint
      type: deterministic
      action: run_quality_gates
      retry: 2
      next: merge

    - id: merge
      type: deterministic
      action: merge_queue
      next: complete

    - id: complete
      type: deterministic
      action: mark_complete
  ```

  Files: `internal/harness/blueprint/defaults/feature.yaml`, `internal/harness/blueprint/defaults/stream.yaml`, `internal/harness/blueprint/defaults/hotfix.yaml`

- [x] **1.4** Create blueprint registry
  Create `internal/harness/blueprint/registry.go` with:

  ```go
  package blueprint

  // Registry holds loaded blueprints and provides lookup.
  type Registry struct {
      blueprints map[string]*Blueprint
      mu         sync.RWMutex
  }

  func NewRegistry() *Registry

  // LoadDefaults loads the shipped default blueprints from the embedded defaults/ directory.
  // Use go:embed to embed the defaults/ directory.
  func (r *Registry) LoadDefaults() error

  // LoadFromDir loads blueprints from a directory (e.g., .deck/blueprints/ or ~/.config/deck/blueprints/).
  // Blueprints loaded later override earlier ones with the same name.
  func (r *Registry) LoadFromDir(dir string) error

  // Get returns a blueprint by name. Returns nil, false if not found.
  func (r *Registry) Get(name string) (*Blueprint, bool)

  // GetDefault returns the blueprint with trigger "default". Returns nil, false if none.
  func (r *Registry) GetDefault() (*Blueprint, bool)

  // List returns all registered blueprint names.
  func (r *Registry) List() []string
  ```

  Use `embed.FS` with `//go:embed defaults/*.yaml` to embed shipped defaults.
  `LoadDefaults`: iterate embedded files, unmarshal each, validate, store by name.
  `LoadFromDir`: call `LoadDir`, merge into registry (overwriting existing names).

  Import: `embed`, `sync`, `sort`.
  File: `internal/harness/blueprint/registry.go`

---

## Phase 2: Blueprint Engine (State Machine)

- [ ] **2.1** Create blueprint execution engine
  Create `internal/harness/blueprint/engine.go` with:

  ```go
  package blueprint

  // Execution represents a running blueprint instance tied to an objective.
  type Execution struct {
      ID          string                `json:"id"`
      BlueprintName string             `json:"blueprint_name"`
      ObjectiveID string               `json:"objective_id"`
      CurrentStep string               `json:"current_step"`
      StepStates  map[string]*StepState `json:"step_states"`
      Status      string               `json:"status"` // "running", "completed", "failed", "waiting_human"
      CreatedAt   time.Time            `json:"created_at"`
      UpdatedAt   time.Time            `json:"updated_at"`
  }

  // StepHandler is called by the engine when a step needs to execute.
  // The engine itself does NOT implement agent spawning, merging, etc.
  // Instead, callers register handlers for each step type.
  type StepHandler func(ctx context.Context, exec *Execution, step *Step) (StepResult, error)

  type StepResult struct {
      Status StepStatus `json:"status"`
      Error  string     `json:"error,omitempty"`
      Output string     `json:"output,omitempty"`
  }

  // Engine drives blueprint execution.
  type Engine struct {
      registry *Registry
      handlers map[StepType]StepHandler
      logger   *slog.Logger
  }

  func NewEngine(registry *Registry, logger *slog.Logger) *Engine

  // RegisterHandler registers a handler for a step type.
  func (e *Engine) RegisterHandler(stepType StepType, handler StepHandler)

  // Start creates a new Execution for the given blueprint and objective,
  // initializes all step states to "pending", sets the first step as current,
  // and returns the execution. Does NOT advance — call Advance() to begin.
  func (e *Engine) Start(ctx context.Context, blueprintName string, objectiveID string) (*Execution, error)

  // Advance moves the execution forward by running the current step's handler.
  // If the step completes, it advances to the next step (via step.Next).
  // If the step fails and has retries remaining, it retries.
  // If the step is "human" type, it sets status to "waiting_human" and returns.
  // Returns the updated execution.
  func (e *Engine) Advance(ctx context.Context, exec *Execution) (*Execution, error)

  // ApproveHuman unblocks a "waiting_human" execution and advances to the next step.
  func (e *Engine) ApproveHuman(ctx context.Context, exec *Execution) (*Execution, error)

  // GetStepByID returns the step definition from the blueprint.
  func (e *Engine) GetStepByID(bp *Blueprint, stepID string) (*Step, error)
  ```

  The engine is a **synchronous state machine driver**. It does not manage goroutines or long-running processes — that's the dispatcher's job (Phase 4). The engine simply:
  1. Looks up the current step
  2. Calls the registered handler
  3. Updates step state based on result
  4. Advances to `step.Next` or handles retries/failures

  Import: `context`, `time`, `log/slog`, `fmt`, `github.com/google/uuid`.
  File: `internal/harness/blueprint/engine.go`

- [ ] **2.2** Create blueprint persistence (DB store)
  Create `internal/db/blueprints.go` with:

  ```go
  package db

  // ExecutionStore persists blueprint execution state.
  type ExecutionStore struct { db *sql.DB }

  func NewExecutionStore(db *sql.DB) *ExecutionStore

  // Create inserts a new execution record.
  func (s *ExecutionStore) Create(ctx context.Context, exec *blueprint.Execution) error

  // Get retrieves an execution by ID.
  func (s *ExecutionStore) Get(ctx context.Context, id string) (*blueprint.Execution, error)

  // GetByObjective retrieves the execution for a given objective.
  func (s *ExecutionStore) GetByObjective(ctx context.Context, objectiveID string) (*blueprint.Execution, error)

  // Update saves the current execution state (current_step, step_states JSON, status, updated_at).
  func (s *ExecutionStore) Update(ctx context.Context, exec *blueprint.Execution) error

  // List returns all executions, ordered by created_at desc.
  func (s *ExecutionStore) List(ctx context.Context) ([]blueprint.Execution, error)
  ```

  `StepStates` is stored as a JSON blob in a TEXT column.
  Timestamps stored as Unix seconds (matching Phase 1 pattern).

  Add the migration for the `executions` table to `internal/db/migrations.go`:
  ```sql
  CREATE TABLE IF NOT EXISTS executions (
      id TEXT PRIMARY KEY,
      blueprint_name TEXT NOT NULL,
      objective_id TEXT NOT NULL,
      current_step TEXT NOT NULL,
      step_states TEXT NOT NULL DEFAULT '{}',
      status TEXT NOT NULL DEFAULT 'running',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
  );

  CREATE INDEX IF NOT EXISTS idx_executions_objective ON executions(objective_id);
  ```

  Import: `database/sql`, `context`, `encoding/json`, `time`, `fmt`, `github.com/syndg/deck/internal/harness/blueprint`.
  Files: `internal/db/blueprints.go`, `internal/db/migrations.go` (append)

---

## Phase 3: Scoped Rules Engine

- [ ] **3.1** Create rules domain types
  Create `internal/harness/rules/types.go` with:

  ```go
  package rules

  // Rule represents a scoped rule loaded from a markdown file with YAML frontmatter.
  type Rule struct {
      Scope    string   `yaml:"scope"`              // glob pattern (e.g., "src/auth/**")
      Priority string   `yaml:"priority,omitempty"`  // "high", "normal" (default: "normal")
      Tools    *ToolScope `yaml:"tools,omitempty"`   // optional tool restrictions
      Body     string   `yaml:"-"`                   // markdown content after frontmatter
      Source   string   `yaml:"-"`                   // file path this rule was loaded from
  }

  type ToolScope struct {
      Include []string `yaml:"include,omitempty"`
      Exclude []string `yaml:"exclude,omitempty"`
  }

  // MatchedRule is a rule that matched a specific file path, with its source.
  type MatchedRule struct {
      Rule     *Rule
      MatchedOn string // the glob pattern that matched
  }
  ```

  File: `internal/harness/rules/types.go`

- [ ] **3.2** Create rules loader and matcher
  Create `internal/harness/rules/engine.go` with:

  ```go
  package rules

  // Engine loads rules from directories and matches them against file paths.
  type Engine struct {
      rules  []*Rule
      mu     sync.RWMutex
      logger *slog.Logger
  }

  func NewEngine(logger *slog.Logger) *Engine

  // LoadDir loads all .md files from a directory, parsing YAML frontmatter and markdown body.
  // Frontmatter is delimited by "---" lines at the start of the file.
  // Can be called multiple times (e.g., for global + project rules).
  func (e *Engine) LoadDir(dir string) error

  // ParseRule parses a single rule file (frontmatter + body).
  func ParseRule(content []byte, source string) (*Rule, error)

  // Match returns all rules whose scope glob matches any of the given file paths.
  // Results are sorted: high-priority rules first, then by source path.
  func (e *Engine) Match(filePaths []string) []MatchedRule

  // MatchSingle returns all rules whose scope glob matches a single file path.
  func (e *Engine) MatchSingle(filePath string) []MatchedRule

  // All returns all loaded rules.
  func (e *Engine) All() []*Rule

  // BuildContext generates the combined rules text for injection into an agent overlay.
  // High-priority rules are prefixed with "IMPORTANT CONSTRAINT:" header.
  func (e *Engine) BuildContext(filePaths []string) string
  ```

  Frontmatter parsing: split on `---` delimiters, unmarshal YAML portion, keep remainder as Body.
  Glob matching: use `path.Match` or `filepath.Match` for simple globs. For `**` (recursive) patterns, implement a simple recursive matcher or use `doublestar` semantics manually (match any number of path segments).

  Import: `os`, `path/filepath`, `strings`, `bytes`, `sync`, `log/slog`, `sort`, `fmt`, `gopkg.in/yaml.v3`.
  File: `internal/harness/rules/engine.go`

---

## Phase 4: Tool Curator

- [ ] **4.1** Create tool curator types
  Create `internal/harness/tools/types.go` with:

  ```go
  package tools

  // ToolSpec represents a tool that can be provided to an agent.
  type ToolSpec struct {
      Name     string `json:"name"`      // e.g., "mcp:github:create_pr"
      Source   string `json:"source"`    // e.g., "mcp:github", "builtin"
      Category string `json:"category"`  // e.g., "filesystem", "git", "database"
  }

  // CurationResult is the resolved tool set for a specific agent.
  type CurationResult struct {
      Tools    []ToolSpec `json:"tools"`
      Included int        `json:"included"`   // count of tools included
      Excluded int        `json:"excluded"`   // count of tools excluded by rules
      Capped   int        `json:"capped"`     // count of tools dropped by max_per_agent cap
  }

  // CurationInput holds all the inputs for tool resolution.
  type CurationInput struct {
      AvailableTools []ToolSpec        // all tools available in the system
      BlueprintTools *ToolScope        // tools from the current blueprint step (include/exclude)
      RuleTools      []ToolScope       // tools from matched rules
      ConfigAlways   ConfigToolScope   // global always_include / always_exclude from config
      MaxPerAgent    int               // cap from config
  }

  type ToolScope struct {
      Include []string
      Exclude []string
  }

  type ConfigToolScope struct {
      AlwaysInclude []string
      AlwaysExclude []string
  }
  ```

  File: `internal/harness/tools/types.go`

- [ ] **4.2** Create tool curator
  Create `internal/harness/tools/curator.go` with:

  ```go
  package tools

  // Curator resolves the effective tool set for an agent.
  type Curator struct {
      logger *slog.Logger
  }

  func NewCurator(logger *slog.Logger) *Curator

  // Curate resolves the effective tool set from all inputs.
  // Resolution order:
  // 1. Start with all available tools
  // 2. Apply config always_exclude (remove matching)
  // 3. Apply config always_include (ensure present)
  // 4. Apply blueprint step include (if non-empty, filter to only matching)
  // 5. Apply blueprint step exclude (remove matching)
  // 6. Apply rule tool includes (union — add any matching available tools)
  // 7. Apply rule tool excludes (remove matching)
  // 8. Deduplicate
  // 9. Cap at MaxPerAgent (keep always_include tools, drop lowest-priority extras)
  func (c *Curator) Curate(input CurationInput) CurationResult

  // matchGlob checks if a tool name matches a glob pattern.
  // Supports "*" as wildcard segment: "mcp:github:*" matches "mcp:github:create_pr".
  func matchGlob(pattern, name string) bool
  ```

  The curator is stateless — it takes all inputs and produces a result.
  Tool name matching uses `:` as segment separator (not `/`), so `mcp:github:*` matches any tool under the `mcp:github` namespace.

  Import: `log/slog`, `strings`, `sort`.
  File: `internal/harness/tools/curator.go`

---

## Phase 5: Quality Gate Runner

- [ ] **5.1** Create quality gate types and runner
  Create `internal/harness/gates/types.go` with:

  ```go
  package gates

  // Gate represents a single quality gate (a command to run in a sandbox).
  type Gate struct {
      Name    string `yaml:"name" json:"name"`       // e.g., "lint", "test", "build"
      Command string `yaml:"command" json:"command"`  // e.g., "bun run lint", "go test ./..."
      Timeout int    `yaml:"timeout" json:"timeout"`  // seconds, 0 = default (120s)
  }

  type GateResult struct {
      Gate     Gate   `json:"gate"`
      Passed   bool   `json:"passed"`
      ExitCode int    `json:"exit_code"`
      Stdout   string `json:"stdout"`
      Stderr   string `json:"stderr"`
      Duration int    `json:"duration_ms"`
  }

  type RunResult struct {
      AllPassed bool         `json:"all_passed"`
      Results   []GateResult `json:"results"`
  }
  ```

  File: `internal/harness/gates/types.go`

- [ ] **5.2** Create quality gate runner
  Create `internal/harness/gates/runner.go` with:

  ```go
  package gates

  import (
      "context"
      "github.com/syndg/deck/internal/sandbox"
  )

  // Runner executes quality gates inside a sandbox.
  type Runner struct {
      logger *slog.Logger
  }

  func NewRunner(logger *slog.Logger) *Runner

  // Run executes all gates sequentially in the given sandbox.
  // Stops on first failure unless continueOnFailure is true.
  // Each gate runs as a command via sandbox.Exec().
  func (r *Runner) Run(ctx context.Context, sb sandbox.Sandbox, gates []Gate, continueOnFailure bool) (*RunResult, error)

  // RunSingle executes a single gate in the sandbox.
  func (r *Runner) RunSingle(ctx context.Context, sb sandbox.Sandbox, gate Gate) (*GateResult, error)

  // DefaultGates returns the default quality gates from config.
  // Maps simple names to commands: "lint" -> the configured lint command, etc.
  func DefaultGates(gateNames []string, commands map[string]string) []Gate
  ```

  `RunSingle` implementation:
  1. Build `ExecOpts` with timeout from gate (default 120s if 0)
  2. Call `sb.Exec(ctx, gate.Command, opts)`
  3. Build `GateResult` from `ExecResult`
  4. `Passed` = `ExitCode == 0`

  `Run` iterates gates, calls `RunSingle` for each, collects results.

  Import: `context`, `time`, `log/slog`, `github.com/syndg/deck/internal/sandbox`.
  File: `internal/harness/gates/runner.go`

---

## Phase 6: Integration & Wiring

- [ ] **6.1** Wire harness into daemon
  Update `internal/daemon/daemon.go` to:
  1. Add fields: `blueprintRegistry *blueprint.Registry`, `rulesEngine *rules.Engine`, `toolCurator *tools.Curator`, `gateRunner *gates.Runner`
  2. In `New()`: create blueprint registry, load defaults, optionally load from `.deck/blueprints/` and `~/.config/deck/blueprints/`
  3. In `New()`: create rules engine, optionally load from `.deck/rules/` and `~/.config/deck/rules/`
  4. In `New()`: create tool curator and gate runner
  5. Create `ExecutionStore` and add to daemon

  Do NOT add new HTTP routes yet — just wire the harness components so they're available.

  Import the new harness packages.
  File: `internal/daemon/daemon.go`

- [ ] **6.2** Add blueprint API endpoints
  Add to `internal/daemon/routes.go`:

  ```go
  // GET /blueprints — list available blueprints
  func (d *Daemon) handleListBlueprints(w http.ResponseWriter, r *http.Request)

  // GET /blueprints/{name} — get a specific blueprint definition
  func (d *Daemon) handleGetBlueprint(w http.ResponseWriter, r *http.Request)

  // GET /executions — list all executions
  func (d *Daemon) handleListExecutions(w http.ResponseWriter, r *http.Request)

  // GET /executions/{id} — get execution state
  func (d *Daemon) handleGetExecution(w http.ResponseWriter, r *http.Request)
  ```

  Register these routes in `registerRoutes()`.
  These are read-only endpoints for now — execution creation happens through `deck plan` which will be enhanced in Phase 3 (Planning).

  File: `internal/daemon/routes.go`

- [ ] **6.3** Add harness unit tests
  Create test files:

  `internal/harness/blueprint/loader_test.go`:
  - Test `LoadFile` with a valid blueprint YAML
  - Test `Validate` catches: duplicate step IDs, dangling next refs, agent step without role, deterministic step without action
  - Test `LoadDir` loads multiple files

  `internal/harness/blueprint/engine_test.go`:
  - Test `Start` initializes step states correctly
  - Test `Advance` calls the right handler and moves to next step
  - Test `Advance` with retry on failure
  - Test `ApproveHuman` unblocks execution

  `internal/harness/blueprint/registry_test.go`:
  - Test `LoadDefaults` loads the three shipped blueprints
  - Test `GetDefault` returns feature.yaml
  - Test `Get` returns specific blueprints

  `internal/harness/rules/engine_test.go`:
  - Test `ParseRule` extracts frontmatter and body
  - Test `Match` with simple glob
  - Test `Match` with `**` recursive glob
  - Test `BuildContext` with high-priority rules

  `internal/harness/tools/curator_test.go`:
  - Test basic curation with include/exclude
  - Test always_include survives exclusion
  - Test max_per_agent cap
  - Test glob matching (`mcp:github:*`)

  `internal/harness/gates/runner_test.go`:
  - Test `RunSingle` with a mock sandbox (create a simple mock that implements `sandbox.Sandbox`)
  - Test `Run` stops on first failure
  - Test `Run` with continueOnFailure=true

  Files: `internal/harness/blueprint/loader_test.go`, `internal/harness/blueprint/engine_test.go`, `internal/harness/blueprint/registry_test.go`, `internal/harness/rules/engine_test.go`, `internal/harness/tools/curator_test.go`, `internal/harness/gates/runner_test.go`

---

## Execution Order

| Step | Task | Phase |
|------|------|-------|
| 1 | 1.1 Create blueprint domain types | 1 |
| 2 | 1.2 Create blueprint YAML loader | 1 |
| 3 | 1.3 Create shipped default blueprints | 1 |
| 4 | 1.4 Create blueprint registry | 1 |
| 5 | 2.1 Create blueprint execution engine | 2 |
| 6 | 2.2 Create blueprint persistence (DB store) | 2 |
| 7 | 3.1 Create rules domain types | 3 |
| 8 | 3.2 Create rules loader and matcher | 3 |
| 9 | 4.1 Create tool curator types | 4 |
| 10 | 4.2 Create tool curator | 4 |
| 11 | 5.1 Create quality gate types and runner | 5 |
| 12 | 5.2 Create quality gate runner | 5 |
| 13 | 6.1 Wire harness into daemon | 6 |
| 14 | 6.2 Add blueprint API endpoints | 6 |
| 15 | 6.3 Add harness unit tests | 6 |
