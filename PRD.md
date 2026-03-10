# Deck Phase 3: Planning — PRD & Implementation Plan

Build the planning layer that bridges objectives to execution. This phase produces plan and stream data stores, a planning service that manages planner agent sessions, agent overlay generation, objective lifecycle management, plan approval flow, simple mode (single-agent escape hatch), and the CLI/API surface for interacting with plans.

Phase 1 artifacts: `docs/phase1/PRD.md`, `docs/phase1/FINDINGS.md`
Phase 2 artifacts: `docs/phase2/PRD.md`, `docs/phase2/FINDINGS.md`

Atomic tasks for phased implementation. Each task targets 1-3 files max.
Track progress with checkboxes. Log decisions/findings in `FINDINGS.md`.

---

## Phase 1: Plan & Stream Data Layer

The `plans` and `streams` tables already exist in `internal/db/migrations.go` (Phase 1, task 3.2). This phase creates the stores that operate on them.

- [x] **1.1** Create plan store
  Create `internal/db/plans.go` with:

  ```go
  package db

  import (
      "context"
      "database/sql"
      "encoding/json"
      "fmt"
      "time"
      "github.com/google/uuid"
      "github.com/syndg/deck/internal/domain"
  )

  // PlanStore persists plan records.
  type PlanStore struct { db *sql.DB }

  func NewPlanStore(db *sql.DB) *PlanStore

  // Create inserts a new plan. Generates UUID if ID is empty.
  // Stores QualityGates as JSON text. Timestamps as Unix seconds.
  func (s *PlanStore) Create(ctx context.Context, plan *domain.Plan) error

  // Get retrieves a plan by ID.
  func (s *PlanStore) Get(ctx context.Context, id string) (*domain.Plan, error)

  // GetByObjective retrieves the plan for a given objective.
  func (s *PlanStore) GetByObjective(ctx context.Context, objectiveID string) (*domain.Plan, error)

  // List returns all plans, ordered by created_at desc.
  func (s *PlanStore) List(ctx context.Context) ([]domain.Plan, error)

  // UpdateStatus updates a plan's status and updated_at timestamp.
  func (s *PlanStore) UpdateStatus(ctx context.Context, id string, status domain.PlanStatus) error

  // Update saves the full plan state (status, quality_gates, updated_at).
  func (s *PlanStore) Update(ctx context.Context, plan *domain.Plan) error
  ```

  Follow the same patterns established in Phase 1:
  - Generate UUID via `uuid.New().String()` if ID empty
  - Store timestamps as Unix seconds (`time.Now().Unix()`)
  - `QualityGates` stored as JSON text via `json.Marshal`/`json.Unmarshal`
  - Return `fmt.Errorf("plan not found: %s", id)` wrapping `sql.ErrNoRows`

  File: `internal/db/plans.go`

- [x] **1.2** Create stream store
  Create `internal/db/streams.go` with:

  ```go
  package db

  import (
      "context"
      "database/sql"
      "encoding/json"
      "fmt"
      "time"
      "github.com/google/uuid"
      "github.com/syndg/deck/internal/domain"
  )

  // StreamStore persists stream records.
  type StreamStore struct { db *sql.DB }

  func NewStreamStore(db *sql.DB) *StreamStore

  // Create inserts a new stream. Generates UUID if ID is empty.
  // Stores FileScope and Dependencies as JSON text.
  func (s *StreamStore) Create(ctx context.Context, stream *domain.Stream) error

  // Get retrieves a stream by ID.
  func (s *StreamStore) Get(ctx context.Context, id string) (*domain.Stream, error)

  // ListByPlan returns all streams for a plan, ordered by created_at asc.
  func (s *StreamStore) ListByPlan(ctx context.Context, planID string) ([]domain.Stream, error)

  // UpdateStatus updates a stream's status.
  func (s *StreamStore) UpdateStatus(ctx context.Context, id string, status string) error

  // ListReady returns streams whose dependencies are all completed.
  // Resolves dependency graph: a stream is ready if status is "pending"
  // and all stream IDs in its dependencies list have status "completed".
  func (s *StreamStore) ListReady(ctx context.Context, planID string) ([]domain.Stream, error)
  ```

  `FileScope` and `Dependencies` are `[]string` stored as JSON text columns.
  `ListReady` implementation:
  1. Fetch all streams for the plan
  2. Build a map of stream ID → status
  3. Return streams where status is "pending" and all dependency IDs map to "completed"

  File: `internal/db/streams.go`

---

## Phase 2: Objective Lifecycle Manager

Centralize objective state transitions and enforce valid lifecycle progression.

- [x] **2.1** Create objective lifecycle manager
  Create `internal/services/lifecycle/manager.go` with:

  ```go
  package lifecycle

  import (
      "context"
      "fmt"
      "log/slog"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      events "github.com/syndg/deck/internal/services/events"
  )

  // Manager enforces objective state transitions and coordinates
  // plan/stream status propagation.
  type Manager struct {
      objectives *db.ObjectiveStore
      plans      *db.PlanStore
      streams    *db.StreamStore
      agents     *db.AgentStore
      eventBus   *events.PersistentBus
      logger     *slog.Logger
  }

  func New(
      objectives *db.ObjectiveStore,
      plans *db.PlanStore,
      streams *db.StreamStore,
      agents *db.AgentStore,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Manager

  // Transition moves an objective to a new status if the transition is valid.
  // Valid transitions:
  //   planning  → approved, failed
  //   approved  → executing, failed
  //   executing → reviewing, failed
  //   reviewing → completed, failed
  //   failed    → planning (retry)
  // Publishes an EventObjectiveUpdated on success.
  func (m *Manager) Transition(ctx context.Context, objectiveID string, to domain.ObjectiveStatus) error

  // IsValidTransition checks if a status transition is allowed.
  func IsValidTransition(from, to domain.ObjectiveStatus) bool

  // ApprovePlan approves a plan and transitions the objective to "approved".
  // Sets plan status to "approved", objective status to "approved".
  func (m *Manager) ApprovePlan(ctx context.Context, planID string) error

  // RejectPlan rejects a plan and returns the objective to "planning".
  // Sets plan status to "failed", keeps objective in "planning" for re-plan.
  func (m *Manager) RejectPlan(ctx context.Context, planID string) error

  // MarkPlanReady sets a plan to "pending_approval" and publishes an event.
  // Called by the planning service when a planner agent finishes.
  func (m *Manager) MarkPlanReady(ctx context.Context, planID string) error

  // CheckObjectiveCompletion checks if all streams in the objective's plan
  // are completed, and if so, transitions the objective to "reviewing" or
  // "completed" based on autonomy level.
  func (m *Manager) CheckObjectiveCompletion(ctx context.Context, objectiveID string) error
  ```

  State transition map as a `map[ObjectiveStatus][]ObjectiveStatus` for validation.
  Each transition publishes `EventObjectiveUpdated` with the old and new status in the payload.

  File: `internal/services/lifecycle/manager.go`

---

## Phase 3: Agent Overlay Generation

Build the context package that each agent receives: role definition, task spec, file scope, matched rules, quality gates, and communication config.

- [x] **3.1** Create role definitions
  Create `internal/services/agents/roles.go` with:

  ```go
  package agents

  // RoleDefinition describes an agent role's capabilities and constraints.
  type RoleDefinition struct {
      Name        string   // "planner", "lead", "builder", "reviewer", "merger"
      Description string   // human-readable role description
      MaxDepth    int      // hierarchy depth (0=planner, 1=lead, 2=worker)
      CanSpawn    []string // roles this role can spawn (planner→lead, lead→worker)
      Persistent  bool     // true for planner/lead, false for workers
  }

  // DefaultRoles returns the built-in role definitions.
  func DefaultRoles() map[string]*RoleDefinition
  ```

  Default roles:
  - `planner`: depth 0, can spawn `["lead"]`, persistent, "Explores codebase and decomposes objectives into parallel work streams"
  - `lead`: depth 1, can spawn `["builder", "reviewer", "merger"]`, persistent, "Manages a work stream, writes specs, coordinates workers"
  - `builder`: depth 2, can spawn `[]`, ephemeral, "Implements code changes according to spec"
  - `reviewer`: depth 2, can spawn `[]`, ephemeral, "Reviews implementation for correctness and quality"
  - `merger`: depth 1, can spawn `[]`, ephemeral, "Resolves merge conflicts using semantic understanding"
  - `scout`: depth 2, can spawn `[]`, ephemeral, "Explores codebase to gather context for a task"

  File: `internal/services/agents/roles.go`

- [x] **3.2** Create agent overlay builder
  Create `internal/services/agents/overlay.go` with:

  ```go
  package agents

  import (
      "fmt"
      "strings"
      "github.com/syndg/deck/internal/domain"
      "github.com/syndg/deck/internal/harness/rules"
      "github.com/syndg/deck/internal/harness/tools"
  )

  // OverlayInput holds all inputs for constructing an agent's system overlay.
  type OverlayInput struct {
      AgentName    string              // e.g., "builder-auth-1"
      Role         *RoleDefinition
      Objective    *domain.Objective
      Stream       *domain.Stream      // nil for planner
      TaskSpec     string              // from lead or plan description
      FileScope    []string            // files this agent may modify
      MatchedRules []rules.MatchedRule // rules matched against file scope
      CuratedTools tools.CurationResult
      QualityGates []string            // gate commands to run before completion
      LeadAgent    string              // name of this agent's lead (empty for planners)
      Guidance     string              // project-level guidance from .deck/config.yaml
  }

  // BuildOverlay generates the markdown system prompt overlay for an agent.
  // Sections:
  //   1. Agent identity and role
  //   2. Task description
  //   3. File scope (if any)
  //   4. Matched rules (high-priority prefixed with "IMPORTANT CONSTRAINT:")
  //   5. Quality gates
  //   6. Communication config
  //   7. Constraints
  func BuildOverlay(input OverlayInput) string

  // BuildPlannerOverlay generates a simplified overlay for planner agents.
  // Planners get: role, objective description, guidance, and instructions
  // for producing a structured plan with streams, scopes, and dependencies.
  func BuildPlannerOverlay(objective *domain.Objective, guidance string) string
  ```

  `BuildOverlay` produces markdown following the design doc's overlay format:
  ```markdown
  # Deck Agent: {agentName}

  ## Role
  You are a {role.Name} agent. {role.Description}

  ## Task
  Objective: {objective.Description}
  Stream: {stream.Title}
  {taskSpec}

  ## File Scope
  You may ONLY modify these files:
  - {fileScope entries}

  ## Rules
  {matched rules, high-priority first with IMPORTANT CONSTRAINT prefix}

  ## Quality Gates
  Before signaling completion, you MUST pass:
  - {gate commands}

  ## Communication
  - Your lead is: {leadAgent}
  - Use deck.status() to report progress
  - Use deck.escalate() if you're blocked
  - Use deck.done() when finished

  ## Constraints
  - Do NOT modify files outside your scope
  - Do NOT push to git (Deck handles merging)
  - Do NOT install new dependencies without escalating
  - Commit frequently with descriptive messages
  ```

  `BuildPlannerOverlay` instructs the planner to produce a structured plan:
  ```markdown
  # Deck Agent: planner

  ## Role
  You are a Planner agent. Explore the codebase and decompose the objective into parallel work streams.

  ## Objective
  {objective.Description}

  ## Project Guidance
  {guidance}

  ## Instructions
  Produce a structured plan with:
  1. Streams — parallel units of work
  2. File scopes — which files each stream owns (use globs)
  3. Dependencies — which streams must complete before others start
  4. Quality gates — commands to validate each stream

  Output your plan as YAML in the following format:
  {plan YAML schema}
  ```

  Files: `internal/services/agents/overlay.go`

---

## Phase 4: Planning Service

The planning service manages planner agent sessions — spawning them, collecting their output, parsing structured plans, and storing the results.

- [x] **4.1** Create plan decomposition types and parser
  Create `internal/services/planner/decompose.go` with:

  ```go
  package planner

  import (
      "fmt"
      "gopkg.in/yaml.v3"
      "github.com/syndg/deck/internal/domain"
  )

  // RawPlan is the YAML structure a planner agent outputs.
  // Parsed from the agent's output, validated, and converted to domain types.
  type RawPlan struct {
      Streams      []RawStream `yaml:"streams"`
      QualityGates []string    `yaml:"quality_gates"`
  }

  type RawStream struct {
      Title        string   `yaml:"title"`
      Description  string   `yaml:"description"`
      FileScope    []string `yaml:"file_scope"`
      Dependencies []string `yaml:"dependencies"` // stream titles or indices
  }

  // ParsePlan extracts a RawPlan from agent output text.
  // Looks for a YAML block delimited by ```yaml ... ``` markers.
  // Falls back to trying the entire output as YAML.
  func ParsePlan(agentOutput string) (*RawPlan, error)

  // ValidatePlan checks a raw plan for structural correctness:
  // - At least one stream
  // - All streams have a title
  // - All streams have a non-empty file_scope
  // - Dependency references point to existing stream titles
  // - No circular dependencies
  // Returns all validation errors joined.
  func ValidatePlan(plan *RawPlan) error

  // ToDomain converts a RawPlan to domain Plan + Streams.
  // Generates IDs, resolves dependency titles to stream IDs,
  // sets initial statuses.
  func ToDomain(raw *RawPlan, objectiveID string) (*domain.Plan, []domain.Stream)

  // DetectCycles checks the dependency graph for cycles using DFS.
  func DetectCycles(streams []RawStream) error
  ```

  `ParsePlan` strategy:
  1. Find ```yaml ... ``` block in output via string scanning
  2. Unmarshal the YAML content into `RawPlan`
  3. If no code block found, try unmarshalling the entire output
  4. Return error if both fail

  `DetectCycles` uses standard DFS cycle detection on the title→dependencies adjacency list.

  File: `internal/services/planner/decompose.go`

- [x] **4.2** Create planning service
  Create `internal/services/planner/planner.go` with:

  ```go
  package planner

  import (
      "context"
      "fmt"
      "log/slog"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      "github.com/syndg/deck/internal/services/agents"
      "github.com/syndg/deck/internal/services/lifecycle"
      events "github.com/syndg/deck/internal/services/events"
  )

  // Service manages the planning lifecycle for objectives.
  type Service struct {
      plans      *db.PlanStore
      streams    *db.StreamStore
      objectives *db.ObjectiveStore
      agentStore *db.AgentStore
      lifecycle  *lifecycle.Manager
      eventBus   *events.PersistentBus
      logger     *slog.Logger
  }

  func New(
      plans *db.PlanStore,
      streams *db.StreamStore,
      objectives *db.ObjectiveStore,
      agentStore *db.AgentStore,
      lifecycle *lifecycle.Manager,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Service

  // CreatePlan creates a draft plan for an objective and stores it.
  // Called after a planner agent produces a valid plan.
  // 1. Parses and validates the raw plan output
  // 2. Converts to domain types (Plan + Streams)
  // 3. Persists plan and streams
  // 4. Sets plan status to "pending_approval"
  // 5. Publishes EventPlanCreated
  func (s *Service) CreatePlan(ctx context.Context, objectiveID string, agentOutput string) (*domain.Plan, error)

  // CreateSimplePlan creates a single-stream plan for simple mode.
  // No planner agent needed — the objective description becomes the task.
  // Single stream with full file scope ("**/*"), no dependencies.
  func (s *Service) CreateSimplePlan(ctx context.Context, objectiveID string) (*domain.Plan, error)

  // GetPlanWithStreams retrieves a plan and its streams.
  func (s *Service) GetPlanWithStreams(ctx context.Context, planID string) (*domain.Plan, []domain.Stream, error)

  // GetPlanByObjective retrieves the plan for an objective along with streams.
  func (s *Service) GetPlanByObjective(ctx context.Context, objectiveID string) (*domain.Plan, []domain.Stream, error)

  // UpdateStream updates a stream's fields (for plan editing before approval).
  func (s *Service) UpdateStream(ctx context.Context, stream *domain.Stream) error
  ```

  `CreatePlan` flow:
  1. Call `ParsePlan(agentOutput)` to extract structured plan
  2. Call `ValidatePlan(rawPlan)` to validate
  3. Call `ToDomain(rawPlan, objectiveID)` to convert
  4. Store plan via `plans.Create()`
  5. Store each stream via `streams.Create()`
  6. Call `lifecycle.MarkPlanReady()` to set status and publish event

  `CreateSimplePlan` creates a minimal plan:
  - One stream: title = objective description, file_scope = `["**/*"]`, no dependencies
  - Plan status set directly to "pending_approval"
  - Quality gates from config defaults

  File: `internal/services/planner/planner.go`

---

## Phase 5: Simple Mode

Simple mode collapses the planning pipeline to a single agent in a single sandbox — no decomposition, no streams hierarchy, no inter-agent communication.

- [x] **5.1** Create simple mode handler
  Create `internal/services/planner/simple.go` with:

  ```go
  package planner

  import (
      "context"
      "fmt"
      "log/slog"
      "github.com/syndg/deck/internal/domain"
  )

  // SimpleOpts configures simple mode execution.
  type SimpleOpts struct {
      Blueprint    string // blueprint name override (default: "hotfix")
      AutoApprove  bool   // skip approval step
      QualityGates []string // override quality gates (empty = use config defaults)
  }

  // StartSimple creates an objective, generates a single-stream plan,
  // and optionally auto-approves it.
  // Returns the created objective and plan.
  //
  // Flow:
  //   1. Create objective with description
  //   2. Create single-stream plan via CreateSimplePlan
  //   3. If AutoApprove, approve the plan immediately
  //   4. Publish events
  func (s *Service) StartSimple(ctx context.Context, description string, opts SimpleOpts) (*domain.Objective, *domain.Plan, error)
  ```

  Simple mode uses the `hotfix` blueprint by default (single-agent: fix → lint → merge → complete).
  When `AutoApprove` is true, the plan is approved inline — no human gate.
  This is the `deck plan "fix typo" --simple` path from the design doc.

  File: `internal/services/planner/simple.go`

---

## Phase 6: Plan API Endpoints

Expose plan management through the daemon's REST API.

- [x] **6.1** Add plan and stream HTTP routes
  Add to `internal/daemon/routes.go`:

  ```go
  // POST /plans — create a plan from agent output (internal use by planning service)
  func (d *Daemon) handleCreatePlan(w http.ResponseWriter, r *http.Request)

  // GET /plans — list all plans
  func (d *Daemon) handleListPlans(w http.ResponseWriter, r *http.Request)

  // GET /plans/{id} — get a plan with its streams
  func (d *Daemon) handleGetPlan(w http.ResponseWriter, r *http.Request)

  // POST /plans/{id}/approve — approve a plan for execution
  func (d *Daemon) handleApprovePlan(w http.ResponseWriter, r *http.Request)

  // POST /plans/{id}/reject — reject a plan, return objective to planning
  func (d *Daemon) handleRejectPlan(w http.ResponseWriter, r *http.Request)

  // GET /objectives/{id}/plan — get the plan for an objective
  func (d *Daemon) handleGetObjectivePlan(w http.ResponseWriter, r *http.Request)

  // GET /plans/{id}/streams — list streams for a plan
  func (d *Daemon) handleListStreams(w http.ResponseWriter, r *http.Request)

  // GET /streams/{id} — get a single stream
  func (d *Daemon) handleGetStream(w http.ResponseWriter, r *http.Request)
  ```

  Register these routes in `registerRoutes()`:
  ```go
  d.mux.HandleFunc("POST /plans", d.handleCreatePlan)
  d.mux.HandleFunc("GET /plans", d.handleListPlans)
  d.mux.HandleFunc("GET /plans/{id}", d.handleGetPlan)
  d.mux.HandleFunc("POST /plans/{id}/approve", d.handleApprovePlan)
  d.mux.HandleFunc("POST /plans/{id}/reject", d.handleRejectPlan)
  d.mux.HandleFunc("GET /objectives/{id}/plan", d.handleGetObjectivePlan)
  d.mux.HandleFunc("GET /plans/{id}/streams", d.handleListStreams)
  d.mux.HandleFunc("GET /streams/{id}", d.handleGetStream)
  ```

  Response format for `GET /plans/{id}`:
  ```json
  {
    "plan": { "id": "...", "objective_id": "...", "status": "...", ... },
    "streams": [
      { "id": "...", "title": "...", "file_scope": [...], "dependencies": [...], ... }
    ]
  }
  ```

  `handleApprovePlan` calls `lifecycle.ApprovePlan()`.
  `handleRejectPlan` calls `lifecycle.RejectPlan()`.

  File: `internal/daemon/routes.go`

---

## Phase 7: CLI Commands

Extend the CLI client to support plan management.

- [ ] **7.1** Add plan client methods
  Add to `internal/client/client.go`:

  ```go
  // PlanResponse represents a plan with its streams.
  type PlanResponse struct {
      Plan    domain.Plan     `json:"plan"`
      Streams []domain.Stream `json:"streams"`
  }

  // ListPlans returns all plans.
  func (c *Client) ListPlans(ctx context.Context) ([]domain.Plan, error)

  // GetPlan returns a plan with its streams.
  func (c *Client) GetPlan(ctx context.Context, id string) (*PlanResponse, error)

  // GetObjectivePlan returns the plan for an objective.
  func (c *Client) GetObjectivePlan(ctx context.Context, objectiveID string) (*PlanResponse, error)

  // ApprovePlan approves a plan for execution.
  func (c *Client) ApprovePlan(ctx context.Context, planID string) error

  // RejectPlan rejects a plan.
  func (c *Client) RejectPlan(ctx context.Context, planID string) error
  ```

  File: `internal/client/client.go`

- [ ] **7.2** Create `deck plans` command
  Create `cmd/deck/plans.go` with:

  ```go
  var plansCmd = &cobra.Command{
      Use:   "plans",
      Short: "List plans",
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          plans, err := c.ListPlans(cmd.Context())
          // Print table: ID (truncated) | Objective ID (truncated) | Status | Created
      },
  }
  ```

  Format as aligned table:
  ```
  ID        OBJECTIVE   STATUS              CREATED
  abc123    def456      pending_approval    2m ago
  ghi789    jkl012      approved            15m ago
  ```

  Register with `rootCmd.AddCommand(plansCmd)` in init().
  File: `cmd/deck/plans.go`

- [ ] **7.3** Create `deck show` command
  Create `cmd/deck/show.go` with:

  ```go
  var showCmd = &cobra.Command{
      Use:   "show [plan-id]",
      Short: "Show plan details with streams",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          resp, err := c.GetPlan(cmd.Context(), args[0])
          // Print plan summary + stream details
      },
  }
  ```

  Output format:
  ```
  Plan: abc123
  Objective: Refactor auth to JWT
  Status: pending_approval
  Quality Gates: bun test, bun run lint

  Streams:
    1. JWT token generation and validation         [pending]
       Scope: src/auth/token.*, src/auth/jwt.*
       Dependencies: none

    2. Replace session middleware with JWT           [pending]
       Scope: src/middleware/auth.*, src/middleware/session.*
       Dependencies: stream 1

    3. Update all API route handlers                 [pending]
       Scope: src/routes/**/*.ts
       Dependencies: stream 2
  ```

  Register with `rootCmd.AddCommand(showCmd)` in init().
  File: `cmd/deck/show.go`

- [ ] **7.4** Create `deck approve` and `deck reject` commands
  Create `cmd/deck/approve.go` with:

  ```go
  var approveCmd = &cobra.Command{
      Use:   "approve [plan-id]",
      Short: "Approve a plan for execution",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          err := c.ApprovePlan(cmd.Context(), args[0])
          // Print confirmation: "Plan {id} approved. Execution will begin."
      },
  }

  var rejectCmd = &cobra.Command{
      Use:   "reject [plan-id]",
      Short: "Reject a plan and return to planning",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          err := c.RejectPlan(cmd.Context(), args[0])
          // Print confirmation: "Plan {id} rejected. Objective returned to planning."
      },
  }
  ```

  Register both with `rootCmd.AddCommand()` in init().
  File: `cmd/deck/approve.go`

- [ ] **7.5** Enhance `deck plan` with flags
  Update `cmd/deck/plan.go` to support:

  ```go
  var (
      planSimple    bool
      planBlueprint string
      planAuto      bool
  )

  var planCmd = &cobra.Command{
      Use:   "plan [description]",
      Short: "Create a new objective and plan",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)

          if planSimple {
              // POST /objectives with simple=true
              // Auto-creates single-stream plan
              // Print: "Created objective {id} in simple mode."
              // Print: "Plan {planID} auto-approved. Ready for execution."
          } else {
              // Existing behavior: POST /objectives
              // Print: "Created objective {id}: {description}"
              // If planAuto: print "Planner will run in batch mode."
              // Else: print "Planner will start an interactive session."
          }
          return nil
      },
  }

  func init() {
      planCmd.Flags().BoolVar(&planSimple, "simple", false, "single-agent mode (no decomposition)")
      planCmd.Flags().StringVar(&planBlueprint, "blueprint", "", "blueprint to use (default: auto-detect)")
      planCmd.Flags().BoolVar(&planAuto, "auto", false, "batch mode (planner runs autonomously)")
      rootCmd.AddCommand(planCmd)
  }
  ```

  The `--simple` flag triggers `CreateSimplePlan` on the server side.
  The `--auto` flag sets planning mode to batch (planner works autonomously, plan appears in approval queue).
  The `--blueprint` flag overrides blueprint selection.

  File: `cmd/deck/plan.go`

---

## Phase 8: Integration & Wiring

- [ ] **8.1** Wire planning layer into daemon
  Update `internal/daemon/daemon.go` to:
  1. Add fields: `plans *db.PlanStore`, `streams *db.StreamStore`, `planningService *planner.Service`, `lifecycleManager *lifecycle.Manager`
  2. In `New()`: create `PlanStore` and `StreamStore` from DB connection
  3. In `New()`: create `lifecycle.Manager` with all stores and event bus
  4. In `New()`: create `planner.Service` with stores, lifecycle manager, and event bus
  5. Pass planning service and lifecycle manager to route handlers

  Import the new service packages:
  ```go
  "github.com/syndg/deck/internal/services/lifecycle"
  "github.com/syndg/deck/internal/services/planner"
  "github.com/syndg/deck/internal/services/agents"
  ```

  File: `internal/daemon/daemon.go`

- [ ] **8.2** Add objective creation endpoint enhancements
  Update `handleCreateObjective` in `internal/daemon/routes.go` to accept optional fields:

  ```go
  type CreateObjectiveRequest struct {
      Description string `json:"description"`
      Blueprint   string `json:"blueprint,omitempty"`   // blueprint override
      Simple      bool   `json:"simple,omitempty"`      // trigger simple mode
      Auto        bool   `json:"auto,omitempty"`        // batch planning mode
  }
  ```

  When `Simple` is true:
  1. Create objective
  2. Call `planningService.StartSimple()` to create and auto-approve a single-stream plan
  3. Return both objective and plan in response

  When `Auto` is true:
  1. Create objective
  2. Set a flag indicating batch planning mode (stored in objective metadata or a planning_mode field)
  3. Return objective (plan will be created asynchronously by a planner agent in Phase 4)

  File: `internal/daemon/routes.go`

- [ ] **8.3** Add unit tests
  Create test files:

  `internal/db/plans_test.go`:
  - Test `Create` with auto-generated UUID
  - Test `Get` retrieves correct plan
  - Test `GetByObjective` returns the right plan
  - Test `UpdateStatus` changes status
  - Test `List` returns ordered results

  `internal/db/streams_test.go`:
  - Test `Create` with JSON serialization of FileScope/Dependencies
  - Test `Get` round-trips correctly
  - Test `ListByPlan` returns ordered streams
  - Test `UpdateStatus` changes status
  - Test `ListReady` with dependency resolution (setup: 3 streams where stream 3 depends on stream 1 and 2; mark stream 1 completed → stream 3 not ready; mark stream 2 completed → stream 3 ready)

  `internal/services/lifecycle/manager_test.go`:
  - Test valid transitions succeed
  - Test invalid transitions fail (e.g., planning → completed)
  - Test `ApprovePlan` updates both plan and objective status
  - Test `RejectPlan` keeps objective in planning

  `internal/services/planner/decompose_test.go`:
  - Test `ParsePlan` extracts YAML from agent output with code block
  - Test `ParsePlan` handles raw YAML output (no code block)
  - Test `ValidatePlan` catches: no streams, missing title, missing file_scope, dangling dependencies
  - Test `DetectCycles` catches circular dependencies
  - Test `ToDomain` generates correct IDs and resolves dependencies

  `internal/services/planner/planner_test.go`:
  - Test `CreatePlan` end-to-end with mock stores
  - Test `CreateSimplePlan` produces single-stream plan
  - Test `StartSimple` with auto-approve

  `internal/services/agents/overlay_test.go`:
  - Test `BuildOverlay` produces all expected sections
  - Test `BuildOverlay` includes high-priority rules with IMPORTANT prefix
  - Test `BuildPlannerOverlay` includes plan YAML schema
  - Test empty file scope produces no File Scope section

  Files:
  - `internal/db/plans_test.go`
  - `internal/db/streams_test.go`
  - `internal/services/lifecycle/manager_test.go`
  - `internal/services/planner/decompose_test.go`
  - `internal/services/planner/planner_test.go`
  - `internal/services/agents/overlay_test.go`

---

## Execution Order

| Step | Task | Phase |
|------|------|-------|
| 1 | 1.1 Create plan store | 1 |
| 2 | 1.2 Create stream store | 1 |
| 3 | 2.1 Create objective lifecycle manager | 2 |
| 4 | 3.1 Create role definitions | 3 |
| 5 | 3.2 Create agent overlay builder | 3 |
| 6 | 4.1 Create plan decomposition types and parser | 4 |
| 7 | 4.2 Create planning service | 4 |
| 8 | 5.1 Create simple mode handler | 5 |
| 9 | 6.1 Add plan and stream HTTP routes | 6 |
| 10 | 7.1 Add plan client methods | 7 |
| 11 | 7.2 Create `deck plans` command | 7 |
| 12 | 7.3 Create `deck show` command | 7 |
| 13 | 7.4 Create `deck approve` and `deck reject` commands | 7 |
| 14 | 7.5 Enhance `deck plan` with flags | 7 |
| 15 | 8.1 Wire planning layer into daemon | 8 |
| 16 | 8.2 Add objective creation endpoint enhancements | 8 |
| 17 | 8.3 Add unit tests | 8 |
