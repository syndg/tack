# Deck Phase 4: Execution — PRD & Implementation Plan

Build the execution engine that transforms approved plans into running agent teams. This phase implements the mail broker, local sandbox provider, Claude Code agent runtime, agent spawner, stream scheduler, blueprint step handlers, execution coordinator, and the API/CLI surface for managing execution.

Phase 1 artifacts: `docs/phase1/PRD.md`, `docs/phase1/FINDINGS.md`
Phase 2 artifacts: `docs/phase2/PRD.md`, `docs/phase2/FINDINGS.md`
Phase 3 artifacts: `docs/phase3/PRD.md`, `docs/phase3/FINDINGS.md`

Atomic tasks for phased implementation. Each task targets 1-3 files max.
Track progress with checkboxes. Log decisions/findings in `FINDINGS.md`.

---

## Phase 1: Mail Broker Service

The `mail` table and `MailStore` already exist from Phase 1 (`internal/db/mail.go`). This phase adds the service layer that handles broadcast resolution, event publication, and the HTTP API that agents call to send and receive mail.

- [x] **1.1** Create mail broker service
  Create `internal/services/mail/broker.go` with:

  ```go
  package mail

  import (
      "context"
      "fmt"
      "log/slog"
      "strings"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      events "github.com/syndg/deck/internal/services/events"
  )

  // Broker manages inter-agent mail delivery and broadcast resolution.
  type Broker struct {
      mail     *db.MailStore
      agents   *db.AgentStore
      eventBus *events.PersistentBus
      logger   *slog.Logger
  }

  func New(
      mail *db.MailStore,
      agents *db.AgentStore,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Broker

  // Send delivers a message from one agent to another.
  // If `msg.To` is a broadcast address (starts with "@"), delegates to SendBroadcast.
  // Publishes EventMailSent after persisting.
  func (b *Broker) Send(ctx context.Context, msg *domain.MailMessage) error

  // SendBroadcast resolves a broadcast address and delivers to all matching agents.
  // Broadcast addresses:
  //   @all           — all active agents for the objective
  //   @stream:{id}   — all agents assigned to the given stream
  //   @builders      — all agents with role "worker" (builder sub-role)
  //   @leads         — all agents with role "lead"
  //   @human         — special: publish EventEscalation, do not deliver to agents
  func (b *Broker) SendBroadcast(ctx context.Context, from, broadcastAddr, msgType, payload, objectiveID, streamID string) error

  // GetUnread retrieves unread messages for an agent, ordered by creation time.
  // Delegates to MailStore.GetUnread().
  func (b *Broker) GetUnread(ctx context.Context, agentName string) ([]domain.MailMessage, error)

  // MarkRead marks a single message as read.
  func (b *Broker) MarkRead(ctx context.Context, messageID int64) error

  // MarkAllRead marks all unread messages for an agent as read.
  func (b *Broker) MarkAllRead(ctx context.Context, agentName string) error

  // IsBroadcast returns true if the address is a broadcast group (@all, @leads, etc).
  func IsBroadcast(addr string) bool
  ```

  Broadcast resolution for `SendBroadcast`:
  1. Query agents via `agents.ListByObjective(ctx, objectiveID)`
  2. Filter by broadcast address:
     - `@all`: no filter — deliver to all
     - `@stream:{streamID}`: match `agent.StreamID == streamID`
     - `@builders`: match `agent.Role == "worker"`
     - `@leads`: match `agent.Role == "lead"`
     - `@human`: publish `EventEscalation` event with payload, return immediately
  3. Fan-out: create individual `MailMessage` per recipient, call `mail.Send()` for each
  4. Publish `EventMailSent` with count of recipients

  Add new event type constants to `internal/domain/types.go`:
  ```go
  EventMailSent    EventType = "mail.sent"
  EventEscalation  EventType = "escalation"
  ```

  Files: `internal/services/mail/broker.go`, `internal/domain/types.go`

- [x] **1.2** Add mail HTTP routes
  Add to `internal/daemon/routes.go`:

  ```go
  // POST /mail — send a message (used by agent extensions)
  // Body: {"from": "...", "to": "...", "type": "...", "payload": "...", "objective": "...", "stream": "..."}
  // If "to" starts with "@", treated as broadcast.
  func (d *Daemon) handleSendMail(w http.ResponseWriter, r *http.Request)

  // GET /mail/{agentName}/unread — get unread messages for an agent
  // Returns JSON array of MailMessage.
  func (d *Daemon) handleGetUnreadMail(w http.ResponseWriter, r *http.Request)

  // POST /mail/{id}/read — mark a single message as read
  func (d *Daemon) handleMarkMailRead(w http.ResponseWriter, r *http.Request)

  // POST /mail/{agentName}/read-all — mark all messages as read for an agent
  func (d *Daemon) handleMarkAllMailRead(w http.ResponseWriter, r *http.Request)
  ```

  Register routes in `registerRoutes()`:
  ```go
  d.mux.HandleFunc("POST /mail", d.handleSendMail)
  d.mux.HandleFunc("GET /mail/{agentName}/unread", d.handleGetUnreadMail)
  d.mux.HandleFunc("POST /mail/{id}/read", d.handleMarkMailRead)
  d.mux.HandleFunc("POST /mail/{agentName}/read-all", d.handleMarkAllMailRead)
  ```

  `handleSendMail` decodes the JSON body into a `MailMessage`, detects broadcast addresses
  (via `mail.IsBroadcast(msg.To)`), and delegates to `broker.Send()` or `broker.SendBroadcast()`.

  `handleGetUnreadMail` uses `r.PathValue("agentName")` and returns JSON array.

  `handleMarkMailRead` parses the message ID from `r.PathValue("id")` as int64.

  Add `mailBroker *mail.Broker` field to `Daemon` struct (nil until task 8.1 wires it).

  File: `internal/daemon/routes.go`

---

## Phase 2: Provider Implementations

Implement the first concrete sandbox provider and agent runtime. The local sandbox provider uses git worktrees for isolation. The Claude Code runtime spawns `claude -p` in headless mode.

- [x] **2.1** Implement local sandbox provider
  Create `internal/sandbox/local/provider.go` with:

  ```go
  package local

  import (
      "context"
      "fmt"
      "log/slog"
      "os"
      "os/exec"
      "path/filepath"
      "sync"
      "github.com/google/uuid"
      "github.com/syndg/deck/internal/sandbox"
  )

  // Provider creates sandboxes as local git worktrees.
  // Each sandbox is an isolated worktree with its own branch.
  type Provider struct {
      repoRoot    string          // path to the main git repository
      worktreeDir string          // base directory for worktrees
      mu          sync.Mutex
      sandboxes   map[string]*LocalSandbox
      logger      *slog.Logger
  }

  func New(repoRoot string, worktreeDir string, logger *slog.Logger) *Provider

  // Create provisions a new git worktree sandbox.
  // 1. Generate sandbox ID (uuid)
  // 2. Create branch: deck/{labels["deck.objective"][:8]}/{labels["deck.role"]}-{id[:8]}
  // 3. Run: git worktree add {worktreeDir}/{id} -b {branch}
  // 4. Apply env vars from opts.EnvVars
  // 5. Track sandbox in internal map
  func (p *Provider) Create(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error)

  // Get retrieves a sandbox by ID from the internal map.
  func (p *Provider) Get(ctx context.Context, id string) (sandbox.Sandbox, error)

  // List returns sandboxes matching the given label filters.
  // A sandbox matches if all provided labels match (AND logic).
  func (p *Provider) List(ctx context.Context, labels map[string]string) ([]sandbox.Sandbox, error)

  // Delete removes the worktree and its branch.
  // Runs: git worktree remove {path} --force
  // Runs: git branch -D {branch}
  func (p *Provider) Delete(ctx context.Context, id string) error
  ```

  `LocalSandbox` implements `sandbox.Sandbox`:
  ```go
  type LocalSandbox struct {
      id      string
      path    string                // worktree absolute path
      branch  string                // git branch name
      labels  map[string]string
      status  sandbox.SandboxStatus
      envVars map[string]string
      mu      sync.Mutex
  }

  func (s *LocalSandbox) ID() string
  func (s *LocalSandbox) Status() sandbox.SandboxStatus

  // Exec runs a command in the worktree directory via exec.CommandContext.
  // Applies sandbox env vars. Uses opts.WorkDir relative to worktree if provided.
  // Returns ExecResult with stdout, stderr, and exit code.
  func (s *LocalSandbox) Exec(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error)

  // Upload writes content to a file within the worktree.
  func (s *LocalSandbox) Upload(ctx context.Context, content []byte, path string) error

  // Download reads a file from the worktree.
  func (s *LocalSandbox) Download(ctx context.Context, path string) ([]byte, error)

  // Stop sets status to "stopped". No-op for local worktrees (they persist until Delete).
  func (s *LocalSandbox) Stop(ctx context.Context) error

  // Start sets status to "running". Only valid if currently "stopped".
  func (s *LocalSandbox) Start(ctx context.Context) error
  ```

  `Exec` implementation:
  1. Parse command string with `sh -c` for shell execution
  2. Set `cmd.Dir` to worktree path (or `opts.WorkDir` if provided)
  3. Merge sandbox env vars + opts.Env into `cmd.Env`
  4. Capture stdout and stderr via `bytes.Buffer`
  5. Run with context for cancellation support
  6. Return `ExecResult{ExitCode, Stdout, Stderr}`

  File: `internal/sandbox/local/provider.go`

- [x] **2.2** Implement Claude Code agent runtime
  Create `internal/runtime/claudecode/runtime.go` with:

  ```go
  package claudecode

  import (
      "context"
      "fmt"
      "log/slog"
      "strings"
      "sync"
      "github.com/syndg/deck/internal/runtime"
      "github.com/syndg/deck/internal/sandbox"
  )

  // Runtime spawns Claude Code agents in sandboxes via `claude -p`.
  type Runtime struct {
      model  string // default model (e.g., "sonnet")
      logger *slog.Logger
  }

  func New(model string, logger *slog.Logger) *Runtime

  func (r *Runtime) Name() string         // returns "claude-code"
  func (r *Runtime) SupportsRPC() bool     // returns false
  func (r *Runtime) SupportsHooks() bool   // returns true

  // Spawn starts a Claude Code process in the sandbox.
  // 1. Build prompt: overlay + "\n\nBegin your task now."
  // 2. Build tool list from opts.Tools
  // 3. Build command: claude -p "{prompt}" --model {model} --allowedTools {tools}
  // 4. Set env vars: DECK_DAEMON_URL, DECK_AGENT_TOKEN, plus opts.EnvVars
  // 5. Execute via sandbox.Exec() in a goroutine
  // 6. Return ClaudeCodeProcess that monitors execution
  func (r *Runtime) Spawn(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error)
  ```

  `ClaudeCodeProcess` implements `runtime.AgentProcess`:
  ```go
  type ClaudeCodeProcess struct {
      sandbox  sandbox.Sandbox
      cancel   context.CancelFunc
      doneCh   chan struct{}
      result   runtime.AgentResult
      outputCh chan runtime.AgentEvent
      mu       sync.Mutex
      killed   bool
  }

  // Send is a no-op for Claude Code (no mid-execution RPC support).
  // Returns nil without error.
  func (p *ClaudeCodeProcess) Send(ctx context.Context, msg runtime.AgentMessage) error

  // Output returns the channel that receives agent events.
  // Events are emitted when the process produces output or completes.
  func (p *ClaudeCodeProcess) Output() <-chan runtime.AgentEvent

  // Wait blocks until the Claude Code process completes and returns the result.
  func (p *ClaudeCodeProcess) Wait() (runtime.AgentResult, error)

  // Kill terminates the process by cancelling the context.
  func (p *ClaudeCodeProcess) Kill() error
  ```

  `Spawn` implementation:
  1. Create cancellable context from parent
  2. Build the full prompt: `opts.Overlay + "\n\n" + "Begin your task now."`
  3. Build tool allowlist: join `opts.Tools` with commas
  4. Construct command: `claude -p "..." --model {model} --allowedTools "..."`
  5. Set up env vars map with `DECK_DAEMON_URL` and `DECK_AGENT_TOKEN` from `opts.EnvVars`
  6. Start goroutine that calls `sandbox.Exec()` with the command
  7. On completion: parse exit code, set result (Success = exitCode == 0), send event, close channels
  8. Return `ClaudeCodeProcess` immediately

  File: `internal/runtime/claudecode/runtime.go`

---

## Phase 3: Agent Spawner

The agent spawner coordinates the full lifecycle of creating an agent: recording the session, assembling the overlay (with rules + tools injection), provisioning the sandbox, and starting the agent process.

- [x] **3.1** Create agent spawner service
  Create `internal/services/dispatch/spawner.go` with:

  ```go
  package dispatch

  import (
      "context"
      "fmt"
      "log/slog"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      "github.com/syndg/deck/internal/runtime"
      "github.com/syndg/deck/internal/sandbox"
      "github.com/syndg/deck/internal/services/agents"
      "github.com/syndg/deck/internal/harness/rules"
      "github.com/syndg/deck/internal/harness/tools"
      events "github.com/syndg/deck/internal/services/events"
  )

  // SpawnRequest describes what agent to create.
  type SpawnRequest struct {
      Objective   *domain.Objective
      Stream      *domain.Stream   // nil for planner agents
      Role        string           // "planner", "lead", "builder", "reviewer", "scout"
      TaskSpec    string           // task description or spec content
      ParentAgent string           // name of parent agent (empty for top-level)
      Guidance    string           // project-level guidance from config
  }

  // SpawnResult contains the created agent session, process, and sandbox.
  type SpawnResult struct {
      Session *domain.AgentSession
      Process runtime.AgentProcess
      Sandbox sandbox.Sandbox
  }

  // Spawner creates and manages agent processes.
  type Spawner struct {
      agentStore  *db.AgentStore
      rt          runtime.AgentRuntime
      sp          sandbox.SandboxProvider
      rulesEngine *rules.Engine
      toolCurator *tools.Curator
      eventBus    *events.PersistentBus
      logger      *slog.Logger
  }

  func NewSpawner(
      agentStore *db.AgentStore,
      rt runtime.AgentRuntime,
      sp sandbox.SandboxProvider,
      rulesEngine *rules.Engine,
      toolCurator *tools.Curator,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Spawner

  // Spawn creates a sandbox, assembles the overlay, and starts an agent.
  // Flow:
  //   1. Look up role definition via agents.DefaultRoles()
  //   2. Create AgentSession record (status "pending")
  //   3. Provision sandbox via provider:
  //      - Name: "deck-{objectiveID[:8]}-{role}-{sessionID[:8]}"
  //      - Labels: deck.objective, deck.stream, deck.role
  //      - Ephemeral: !role.Persistent
  //   4. Match rules against file scope (empty for planners)
  //   5. Curate tools for the role + file scope
  //   6. Build agent overlay:
  //      - Planners: agents.BuildPlannerOverlay()
  //      - Others: agents.BuildOverlay() with full OverlayInput
  //   7. Spawn agent process via runtime
  //   8. Update session: status "running", sandbox ID set
  //   9. Publish EventAgentSpawned
  // Returns SpawnResult with session, process, and sandbox.
  func (s *Spawner) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error)

  // Kill terminates an agent process and updates its session status.
  // Marks session as "failed" and publishes EventAgentFailed.
  func (s *Spawner) Kill(ctx context.Context, sessionID string) error
  ```

  Environment variables passed to the agent:
  ```
  DECK_DAEMON_URL=http://localhost:{port}
  DECK_AGENT_TOKEN={generated-uuid}
  DECK_OBJECTIVE_ID={objectiveID}
  DECK_STREAM_ID={streamID}
  DECK_AGENT_ROLE={role}
  ```

  The spawner does NOT track running processes internally — it returns the `SpawnResult`
  and the coordinator is responsible for monitoring process completion.

  File: `internal/services/dispatch/spawner.go`

---

## Phase 4: Stream Scheduler

Dependency-aware scheduler that determines which streams are ready for execution, manages concurrent agent limits, and tracks stream execution state transitions.

- [x] **4.1** Create stream scheduler
  Create `internal/services/dispatch/scheduler.go` with:

  ```go
  package dispatch

  import (
      "context"
      "fmt"
      "log/slog"
      "sync"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      events "github.com/syndg/deck/internal/services/events"
  )

  // Scheduler manages dependency-aware stream execution.
  type Scheduler struct {
      streams       *db.StreamStore
      plans         *db.PlanStore
      maxConcurrent int
      mu            sync.Mutex
      activeStreams  map[string]bool // streamID → executing
      eventBus      *events.PersistentBus
      logger        *slog.Logger
  }

  func NewScheduler(
      streams *db.StreamStore,
      plans *db.PlanStore,
      maxConcurrent int,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Scheduler

  // GetReadyStreams returns streams that are ready to execute:
  //   - Status is "pending"
  //   - All dependency streams have status "completed"
  //   - Total active streams is under maxConcurrent
  // Uses StreamStore.ListReady() for dependency resolution, then caps by concurrency limit.
  func (s *Scheduler) GetReadyStreams(ctx context.Context, planID string) ([]domain.Stream, error)

  // MarkExecuting marks a stream as actively executing and tracks it.
  // Updates stream status to "executing" via StreamStore.UpdateStatus().
  func (s *Scheduler) MarkExecuting(ctx context.Context, streamID string) error

  // MarkCompleted marks a stream as completed and removes from active set.
  // 1. Update stream status to "completed"
  // 2. Remove from activeStreams map
  // 3. Check for newly unblocked streams in the same plan
  // 4. Publish EventStreamReady for each newly ready stream
  func (s *Scheduler) MarkCompleted(ctx context.Context, streamID string, planID string) error

  // MarkFailed marks a stream as failed and removes from active set.
  func (s *Scheduler) MarkFailed(ctx context.Context, streamID string) error

  // ActiveCount returns the number of currently executing streams.
  func (s *Scheduler) ActiveCount() int

  // CanScheduleMore returns true if under the concurrent stream limit.
  func (s *Scheduler) CanScheduleMore() bool
  ```

  Add new event type constant to `internal/domain/types.go`:
  ```go
  EventStreamReady EventType = "stream.ready"
  ```

  `MarkCompleted` cascade logic:
  1. Call `streams.UpdateStatus(ctx, streamID, "completed")`
  2. Remove streamID from `activeStreams`
  3. Fetch the plan to get planID
  4. Call `streams.ListReady(ctx, planID)` to get newly unblocked streams
  5. For each ready stream not already in `activeStreams`, publish `EventStreamReady` with
     `{"stream_id": streamID, "plan_id": planID}` in the event payload

  Files: `internal/services/dispatch/scheduler.go`, `internal/domain/types.go`

---

## Phase 5: Blueprint Step Handlers

Register handlers for each blueprint step type. These handlers implement the actual work performed at each step in the blueprint state machine.

- [x] **5.1** Implement deterministic and human step handlers
  Create `internal/services/dispatch/handlers.go` with:

  ```go
  package dispatch

  import (
      "context"
      "encoding/json"
      "fmt"
      "log/slog"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      "github.com/syndg/deck/internal/harness/blueprint"
      "github.com/syndg/deck/internal/harness/gates"
      "github.com/syndg/deck/internal/services/lifecycle"
  )

  // Handlers implements blueprint step handlers for deterministic and human steps.
  type Handlers struct {
      scheduler   *Scheduler
      gateRunner  *gates.Runner
      lifecycle   *lifecycle.Manager
      plans       *db.PlanStore
      streams     *db.StreamStore
      objectives  *db.ObjectiveStore
      executions  *db.ExecutionStore
      logger      *slog.Logger
  }

  func NewHandlers(
      scheduler *Scheduler,
      gateRunner *gates.Runner,
      lifecycle *lifecycle.Manager,
      plans *db.PlanStore,
      streams *db.StreamStore,
      objectives *db.ObjectiveStore,
      executions *db.ExecutionStore,
      logger *slog.Logger,
  ) *Handlers

  // HandleDeterministic routes to the correct handler based on step.Action.
  // This function is registered with the blueprint engine as the StepTypeDeterministic handler.
  // Actions:
  //   "dispatch_streams" → spawns lead agents for ready streams
  //   "run_quality_gates" → runs quality gates in sandbox
  //   "signal_merge_ready" → marks stream as merge-ready
  //   "mark_complete" → transitions objective to reviewing/completed
  //   "merge_queue" → enqueues for merge (stub for Phase 5)
  func (h *Handlers) HandleDeterministic(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error)

  // HandleHuman pauses execution until human approval.
  // Returns StepResult with status "waiting_human".
  // The blueprint engine sets execution status to "waiting_human" and stops advancing.
  // Execution resumes when ApproveHuman() is called via the API.
  func (h *Handlers) HandleHuman(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error)
  ```

  `HandleDeterministic` action implementations:

  **dispatch_streams:**
  1. Get objective from `exec.ObjectiveID`
  2. Get plan for the objective
  3. Get ready streams via `scheduler.GetReadyStreams()`
  4. For each ready stream, mark as executing via `scheduler.MarkExecuting()`
  5. Publish `EventStreamReady` for each (the coordinator listens and spawns agents)
  6. Return `StepResult{Status: "completed"}`

  **run_quality_gates:**
  1. Get the plan's quality gates
  2. Parse gates into `[]gates.Gate` structs
  3. Look up the sandbox for the current stream's agent (via execution metadata or step context)
  4. Run gates via `gateRunner.Run()` in the sandbox
  5. Return completed if all pass, failed if any fail

  **signal_merge_ready:**
  1. Get the stream ID from execution context
  2. Update stream status to "merge_ready"
  3. Publish `EventMergeQueued` with stream and branch info
  4. Return `StepResult{Status: "completed"}`

  **mark_complete:**
  1. Call `lifecycle.Transition(ctx, exec.ObjectiveID, domain.ObjectiveStatusCompleted)`
     (or `ObjectiveStatusReviewing` based on autonomy — default to reviewing for now)
  2. Return `StepResult{Status: "completed"}`

  **merge_queue:**
  1. Stub implementation for Phase 5 — log and return completed
  2. Phase 5 will implement the actual merge queue processing

  **HandleHuman:**
  1. Log that execution is paused waiting for human approval
  2. Return `StepResult{Status: "waiting_human"}`
  3. The engine's `Advance()` method handles the rest — it detects "waiting_human"
     and sets the execution status accordingly

  File: `internal/services/dispatch/handlers.go`

---

## Phase 6: Execution Coordinator

The coordinator is the central orchestration loop. It subscribes to events, drives blueprint execution, manages agent lifecycle, and coordinates stream completion cascades.

- [x] **6.1** Create execution coordinator
  Create `internal/services/dispatch/coordinator.go` with:

  ```go
  package dispatch

  import (
      "context"
      "encoding/json"
      "fmt"
      "log/slog"
      "sync"
      "github.com/syndg/deck/internal/db"
      "github.com/syndg/deck/internal/domain"
      "github.com/syndg/deck/internal/harness/blueprint"
      "github.com/syndg/deck/internal/services/lifecycle"
      events "github.com/syndg/deck/internal/services/events"
  )

  // Coordinator orchestrates objective execution from plan approval to completion.
  // It subscribes to events and drives blueprint execution.
  type Coordinator struct {
      engine      *blueprint.Engine
      scheduler   *Scheduler
      spawner     *Spawner
      lifecycle   *lifecycle.Manager
      executions  *db.ExecutionStore
      objectives  *db.ObjectiveStore
      plans       *db.PlanStore
      streams     *db.StreamStore
      eventBus    *events.PersistentBus
      logger      *slog.Logger

      mu          sync.Mutex
      activeExecs map[string]context.CancelFunc // objectiveID → cancel
      agentMap    map[string]*SpawnResult        // sessionID → spawn result
  }

  func NewCoordinator(
      engine *blueprint.Engine,
      scheduler *Scheduler,
      spawner *Spawner,
      lifecycle *lifecycle.Manager,
      executions *db.ExecutionStore,
      objectives *db.ObjectiveStore,
      plans *db.PlanStore,
      streams *db.StreamStore,
      eventBus *events.PersistentBus,
      logger *slog.Logger,
  ) *Coordinator

  // Start subscribes to events and begins processing.
  // Subscribes to:
  //   EventObjectiveUpdated — detect objective transitions to "approved" → start execution
  //   EventStreamReady — spawn lead agents for newly unblocked streams
  // Runs event processing in a background goroutine.
  func (c *Coordinator) Start(ctx context.Context) error

  // Stop cancels all active executions and cleans up.
  func (c *Coordinator) Stop()

  // StartExecution begins blueprint execution for an approved objective.
  // 1. Fetch objective, verify status is "approved"
  // 2. Fetch plan for the objective
  // 3. Determine blueprint name (from objective.Blueprint, default to "feature")
  // 4. Create execution via engine.Start(blueprintName, objectiveID)
  // 5. Transition objective to "executing" via lifecycle
  // 6. Run the execution loop in a goroutine
  func (c *Coordinator) StartExecution(ctx context.Context, objectiveID string) error

  // HandleAgentStep implements the StepHandler for agent-type blueprint steps.
  // Registered with the engine as the StepTypeAgent handler.
  // 1. Determine role from step.Role
  // 2. Build SpawnRequest (objective, stream from exec context, role, task spec)
  // 3. Call spawner.Spawn() to create sandbox + agent
  // 4. Track the spawn result in agentMap
  // 5. Call process.Wait() — blocks until agent completes
  // 6. Update agent session status (completed or failed)
  // 7. Publish EventAgentCompleted or EventAgentFailed
  // 8. Return StepResult based on agent result
  func (c *Coordinator) HandleAgentStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error)

  // HandleBlueprintRefStep implements the StepHandler for blueprint_ref steps.
  // Registered with the engine as the StepTypeBlueprintRef handler.
  // For Phase 4, this handles per-stream execution without nested blueprints:
  // 1. Get the plan and all its streams
  // 2. Spawn lead agents for all ready streams (via spawner)
  // 3. Monitor agent completions via event subscription
  // 4. As leads complete: mark stream completed via scheduler, spawn newly ready leads
  // 5. Block until ALL streams are completed
  // 6. Return StepResult{Status: "completed"}
  func (c *Coordinator) HandleBlueprintRefStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error)
  ```

  Add new event type constant to `internal/domain/types.go`:
  ```go
  EventExecutionStarted EventType = "execution.started"
  ```

  Execution loop (`runExecution`, called by `StartExecution` in a goroutine):
  1. Call `engine.Advance(executionID)` repeatedly
  2. Engine calls the appropriate step handler for each step
  3. Handlers either complete immediately (deterministic) or block (agent, blueprint_ref)
  4. If engine returns "waiting_human", the goroutine exits (resumes on ApproveHuman)
  5. If engine returns "completed" or "failed", clean up and exit

  `HandleBlueprintRefStep` per-stream execution:
  1. Subscribe to `EventAgentCompleted` and `EventAgentFailed` events
  2. Call `scheduler.GetReadyStreams()` to find initially ready streams
  3. For each ready stream: spawn lead via `spawner.Spawn()`, mark executing via scheduler
  4. Wait for events:
     - On `EventAgentCompleted`: identify which stream, call `scheduler.MarkCompleted()`
     - On `EventAgentFailed`: call `scheduler.MarkFailed()`, decide to retry or fail
     - On `EventStreamReady`: spawn lead for the newly ready stream
  5. Loop until all streams are completed or any stream fails
  6. Return StepResult{Status: "completed"} or StepResult{Status: "failed"}

  Files: `internal/services/dispatch/coordinator.go`, `internal/domain/types.go`

---

## Phase 7: API & CLI

Extend the daemon API and CLI to support execution management, agent monitoring, and mail interaction.

- [ ] **7.1** Add execution management HTTP routes
  Add to `internal/daemon/routes.go`:

  ```go
  // POST /executions/{id}/approve — approve a human gate step in a blueprint execution
  // Calls engine.ApproveHuman(executionID) to resume execution.
  func (d *Daemon) handleApproveExecution(w http.ResponseWriter, r *http.Request)

  // GET /agents — list all agent sessions
  // Returns JSON array of AgentSession.
  func (d *Daemon) handleListAgents(w http.ResponseWriter, r *http.Request)

  // GET /agents/{id} — get agent session details
  func (d *Daemon) handleGetAgent(w http.ResponseWriter, r *http.Request)

  // POST /agents/{id}/kill — terminate an active agent
  // Calls spawner.Kill(sessionID).
  func (d *Daemon) handleKillAgent(w http.ResponseWriter, r *http.Request)

  // POST /objectives/{id}/execute — manually trigger execution for an approved objective
  // Calls coordinator.StartExecution(objectiveID).
  func (d *Daemon) handleExecuteObjective(w http.ResponseWriter, r *http.Request)
  ```

  Register routes in `registerRoutes()`:
  ```go
  d.mux.HandleFunc("POST /executions/{id}/approve", d.handleApproveExecution)
  d.mux.HandleFunc("GET /agents", d.handleListAgents)
  d.mux.HandleFunc("GET /agents/{id}", d.handleGetAgent)
  d.mux.HandleFunc("POST /agents/{id}/kill", d.handleKillAgent)
  d.mux.HandleFunc("POST /objectives/{id}/execute", d.handleExecuteObjective)
  ```

  Add `coordinator *dispatch.Coordinator` and `spawner *dispatch.Spawner` fields to `Daemon` struct
  (nil until task 8.1 wires them).

  `handleApproveExecution` calls `d.blueprintEngine.ApproveHuman(id)` then
  triggers the coordinator to resume advancing the execution.

  `handleExecuteObjective` verifies the objective exists and is in "approved" status
  before delegating to `coordinator.StartExecution()`.

  File: `internal/daemon/routes.go`

- [ ] **7.2** Add execution and mail client methods
  Add to `internal/client/client.go`:

  ```go
  // ExecuteObjective triggers execution for an approved objective.
  func (c *Client) ExecuteObjective(ctx context.Context, objectiveID string) error

  // ListAgents returns all agent sessions.
  func (c *Client) ListAgents(ctx context.Context) ([]domain.AgentSession, error)

  // GetAgent returns an agent session by ID.
  func (c *Client) GetAgent(ctx context.Context, id string) (*domain.AgentSession, error)

  // KillAgent terminates an active agent.
  func (c *Client) KillAgent(ctx context.Context, id string) error

  // ApproveExecution approves a human gate in a blueprint execution.
  func (c *Client) ApproveExecution(ctx context.Context, executionID string) error

  // ListMail returns unread messages for an agent.
  func (c *Client) ListMail(ctx context.Context, agentName string) ([]domain.MailMessage, error)

  // SendMail sends a message to an agent or broadcast group.
  func (c *Client) SendMail(ctx context.Context, msg *domain.MailMessage) error
  ```

  All methods follow the existing `do()` helper pattern:
  - Build request with appropriate method and path
  - Call `c.do(req)` for POST requests with no response body
  - Decode response JSON for GET requests
  - Wrap errors with context

  File: `internal/client/client.go`

- [ ] **7.3** Create `deck exec` command
  Create `cmd/deck/exec.go` with:

  ```go
  var execCmd = &cobra.Command{
      Use:   "exec [objective-id]",
      Short: "Trigger execution for an approved objective",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          err := c.ExecuteObjective(cmd.Context(), args[0])
          if err != nil {
              return err
          }
          fmt.Printf("Execution started for objective %s.\n", args[0])
          return nil
      },
  }
  ```

  Register with `rootCmd.AddCommand(execCmd)` in init().

  File: `cmd/deck/exec.go`

- [ ] **7.4** Create `deck agents` command
  Create `cmd/deck/agents.go` with:

  ```go
  var agentsCmd = &cobra.Command{
      Use:   "agents",
      Short: "List active agent sessions",
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          sessions, err := c.ListAgents(cmd.Context())
          // Print table: ID | ROLE | OBJECTIVE | STREAM | SANDBOX | STATUS | CREATED
      },
  }

  var killAgentCmd = &cobra.Command{
      Use:   "kill [agent-id]",
      Short: "Terminate an active agent",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          err := c.KillAgent(cmd.Context(), args[0])
          if err != nil {
              return err
          }
          fmt.Printf("Agent %s terminated.\n", args[0])
          return nil
      },
  }
  ```

  Format `agentsCmd` as aligned table using `text/tabwriter`:
  ```
  ID        ROLE      OBJECTIVE   STREAM      STATUS    CREATED
  a1b2c3    lead      d4e5f6      g7h8i9      running   2m ago
  j0k1l2    builder   d4e5f6      g7h8i9      pending   30s ago
  ```

  Register `agentsCmd` with `rootCmd` and `killAgentCmd` as subcommand via init():
  ```go
  func init() {
      agentsCmd.AddCommand(killAgentCmd)
      rootCmd.AddCommand(agentsCmd)
  }
  ```

  Reuse `truncateID` and `timeAgo` helpers from `plans.go` (same `main` package).

  File: `cmd/deck/agents.go`

- [ ] **7.5** Create `deck mail` command
  Create `cmd/deck/mail.go` with:

  ```go
  var mailCmd = &cobra.Command{
      Use:   "mail [agent-name]",
      Short: "View unread mail for an agent",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          msgs, err := c.ListMail(cmd.Context(), args[0])
          // Print each message with details
      },
  }

  var sendMailCmd = &cobra.Command{
      Use:   "send [to] [type] [payload]",
      Short: "Send mail to an agent or broadcast group",
      Args:  cobra.ExactArgs(3),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          // Build MailMessage from args
          err := c.SendMail(cmd.Context(), msg)
          // Print: "Message sent to {to}."
      },
  }
  ```

  `mailCmd` output format:
  ```
  Unread mail for agent "lead-auth":

  #1  FROM: builder-auth-1  TYPE: status  TIME: 2m ago
      JWT token generation complete. Moving to validation layer.

  #2  FROM: @human  TYPE: dispatch  TIME: 5m ago
      Implement JWT refresh token rotation per RFC 7009.
  ```

  Register `mailCmd` with `rootCmd` and `sendMailCmd` as subcommand:
  ```go
  func init() {
      mailCmd.AddCommand(sendMailCmd)
      rootCmd.AddCommand(mailCmd)
  }
  ```

  The `sendMailCmd` requires the `--objective` flag to associate the message with an objective:
  ```go
  var mailObjective string
  sendMailCmd.Flags().StringVar(&mailObjective, "objective", "", "objective ID (required)")
  sendMailCmd.MarkFlagRequired("objective")
  ```

  File: `cmd/deck/mail.go`

---

## Phase 8: Integration & Wiring

- [ ] **8.1** Wire execution layer into daemon
  Update `internal/daemon/daemon.go` to:

  1. Add fields to `Daemon` struct:
  ```go
  mailBroker      *mail.Broker
  sandboxProvider sandbox.SandboxProvider
  agentRuntime    runtime.AgentRuntime
  spawner         *dispatch.Spawner
  scheduler       *dispatch.Scheduler
  coordinator     *dispatch.Coordinator
  ```

  2. Import new packages:
  ```go
  "github.com/syndg/deck/internal/services/mail"
  "github.com/syndg/deck/internal/services/dispatch"
  "github.com/syndg/deck/internal/sandbox/local"
  "github.com/syndg/deck/internal/runtime/claudecode"
  ```

  3. In `New()`:
  ```go
  // Create mail broker
  mailBroker := mail.New(mailStore, agentStore, eventBus, logger)

  // Create sandbox provider (local worktrees for now)
  worktreeDir := filepath.Join(os.TempDir(), "deck-worktrees")
  sandboxProv := local.New(projectRoot, worktreeDir, logger)

  // Create agent runtime (Claude Code)
  agentRuntime := claudecode.New(cfg.Planning.Model, logger)

  // Create spawner
  spawner := dispatch.NewSpawner(agentStore, agentRuntime, sandboxProv, rulesEngine, toolCurator, eventBus, logger)

  // Create scheduler
  scheduler := dispatch.NewScheduler(streamStore, planStore, cfg.Agents.MaxConcurrent, eventBus, logger)

  // Create step handlers
  handlers := dispatch.NewHandlers(scheduler, gateRunner, lifecycleMgr, planStore, streamStore, objectiveStore, executionStore, logger)

  // Register step handlers with blueprint engine
  engine.RegisterHandler(blueprint.StepTypeDeterministic, handlers.HandleDeterministic)
  engine.RegisterHandler(blueprint.StepTypeHuman, handlers.HandleHuman)

  // Create coordinator (also registers agent + blueprint_ref handlers)
  coordinator := dispatch.NewCoordinator(engine, scheduler, spawner, lifecycleMgr, executionStore, objectiveStore, planStore, streamStore, eventBus, logger)
  engine.RegisterHandler(blueprint.StepTypeAgent, coordinator.HandleAgentStep)
  engine.RegisterHandler(blueprint.StepTypeBlueprintRef, coordinator.HandleBlueprintRefStep)
  ```

  4. In `Start()`: call `coordinator.Start(ctx)` to begin event processing.

  5. In `Stop()`: call `coordinator.Stop()` to cancel active executions.

  File: `internal/daemon/daemon.go`

- [ ] **8.2** Add event-driven execution trigger
  Update `internal/daemon/daemon.go` to handle automatic execution triggering:

  The coordinator's `Start()` method already subscribes to `EventObjectiveUpdated`.
  When it receives an event where the objective transitioned to "approved", it checks
  if auto-execution should begin.

  For Phase 4, the trigger flow is:
  1. User calls `POST /plans/{id}/approve` → lifecycle manager transitions objective to "approved"
  2. Lifecycle manager publishes `EventObjectiveUpdated` with `{"from": "planning", "to": "approved"}`
  3. Coordinator receives the event in its event loop
  4. Coordinator calls `StartExecution(objectiveID)` to begin blueprint execution

  Add auto-execution logic to the coordinator's event loop:
  ```go
  // In coordinator.Start(), event processing goroutine:
  case event.Type == domain.EventObjectiveUpdated:
      var payload map[string]string
      json.Unmarshal([]byte(event.Payload), &payload)
      if payload["to"] == string(domain.ObjectiveStatusApproved) {
          go c.StartExecution(ctx, event.Objective)
      }
  ```

  Also handle the `POST /objectives/{id}/execute` manual trigger:
  - Verify objective status is "approved"
  - Call `coordinator.StartExecution(ctx, objectiveID)`
  - Return 200 OK or error

  File: `internal/daemon/daemon.go`

- [ ] **8.3** Add unit tests
  Create test files:

  `internal/services/mail/broker_test.go`:
  - Test `Send` persists message and publishes `EventMailSent`
  - Test `SendBroadcast` with `@all` resolves to all agents for the objective
  - Test `SendBroadcast` with `@stream:{id}` filters agents by stream
  - Test `SendBroadcast` with `@leads` filters agents by role
  - Test `SendBroadcast` with `@human` publishes `EventEscalation` without agent delivery
  - Test `GetUnread` returns only unread messages
  - Test `MarkRead` and `MarkAllRead` update read status
  - Test `IsBroadcast` correctly identifies broadcast addresses

  `internal/services/dispatch/scheduler_test.go`:
  - Test `GetReadyStreams` returns streams with all dependencies satisfied
  - Test `GetReadyStreams` excludes streams with unsatisfied dependencies
  - Test `GetReadyStreams` respects `maxConcurrent` limit
  - Test `MarkExecuting` updates stream status and tracks in active set
  - Test `MarkCompleted` removes from active set and publishes `EventStreamReady` for cascades
  - Test `MarkFailed` updates status and removes from active set
  - Test `CanScheduleMore` returns correct result based on active count

  `internal/services/dispatch/spawner_test.go`:
  - Test `Spawn` creates agent session, sandbox, and process
  - Test `Spawn` assembles overlay with matched rules and curated tools
  - Test `Spawn` uses `BuildPlannerOverlay` for planner role
  - Test `Spawn` publishes `EventAgentSpawned`
  - Test `Kill` terminates process and marks session as failed

  `internal/sandbox/local/provider_test.go`:
  - Test `Create` creates a git worktree with correct branch name
  - Test `Exec` runs commands in the worktree directory
  - Test `Upload` and `Download` round-trip files correctly
  - Test `Delete` removes worktree and branch
  - Test `List` filters sandboxes by labels
  - Test `Get` returns error for non-existent sandbox

  Files:
  - `internal/services/mail/broker_test.go`
  - `internal/services/dispatch/scheduler_test.go`
  - `internal/services/dispatch/spawner_test.go`
  - `internal/sandbox/local/provider_test.go`

---

## Execution Order

| Step | Task | Phase |
|------|------|-------|
| 1 | 1.1 Create mail broker service | 1 |
| 2 | 1.2 Add mail HTTP routes | 1 |
| 3 | 2.1 Implement local sandbox provider | 2 |
| 4 | 2.2 Implement Claude Code agent runtime | 2 |
| 5 | 3.1 Create agent spawner service | 3 |
| 6 | 4.1 Create stream scheduler | 4 |
| 7 | 5.1 Implement deterministic and human step handlers | 5 |
| 8 | 6.1 Create execution coordinator | 6 |
| 9 | 7.1 Add execution management HTTP routes | 7 |
| 10 | 7.2 Add execution and mail client methods | 7 |
| 11 | 7.3 Create `deck exec` command | 7 |
| 12 | 7.4 Create `deck agents` command | 7 |
| 13 | 7.5 Create `deck mail` command | 7 |
| 14 | 8.1 Wire execution layer into daemon | 8 |
| 15 | 8.2 Add event-driven execution trigger | 8 |
| 16 | 8.3 Add unit tests | 8 |
