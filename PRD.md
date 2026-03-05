# Deck Phase 1: Foundation — PRD & Implementation Plan

Establish the Go project structure, core domain types, database layer, event bus, HTTP daemon, and basic CLI. This phase produces a running daemon that accepts HTTP requests, persists data to SQLite, streams events via SSE, and a CLI client that can query status and create objectives.

Atomic tasks for phased implementation. Each task targets 1-2 files max.
Track progress with checkboxes. Log decisions/findings in `FINDINGS.md`.

---

## Phase 1: Project Scaffolding

- [x] **1.1** Initialize Go module and directory structure
  Create `go.mod` with module path `github.com/syndg/deck` and minimum Go 1.22.
  Create the directory tree (empty dirs with `.gitkeep` where needed):
  ```
  cmd/daemon/
  cmd/deck/
  internal/daemon/
  internal/sandbox/
  internal/runtime/
  internal/services/events/
  internal/db/
  internal/config/
  internal/domain/
  internal/client/
  configs/defaults/
  ```
  Create `.gitignore` with Go defaults: `*.exe`, `*.exe~`, `*.dll`, `*.so`, `*.dylib`, `*.test`, `*.out`, `vendor/`, `.env`, `*.db`, `*.db-wal`, `*.db-shm`, `deck`, `deck-daemon`, `/bin/`, `/dist/`.
  Files: `go.mod`, `.gitignore`

- [x] **1.2** Add core dependencies
  Run `go get` to add:
  - `modernc.org/sqlite` — pure Go SQLite driver (no CGo)
  - `github.com/spf13/cobra` — CLI framework
  - `gopkg.in/yaml.v3` — YAML config parsing
  - `github.com/google/uuid` — UUID generation
  Run `go mod tidy` after.
  Files: `go.mod`, `go.sum`

- [x] **1.3** Create entry point stubs
  Create `cmd/daemon/main.go`:
  ```go
  package main

  import "fmt"

  func main() {
      fmt.Println("deck daemon starting...")
  }
  ```
  Create `cmd/deck/main.go`:
  ```go
  package main

  import "fmt"

  func main() {
      fmt.Println("deck cli")
  }
  ```
  Both must compile: `go build ./cmd/daemon` and `go build ./cmd/deck`.
  Files: `cmd/daemon/main.go`, `cmd/deck/main.go`

---

## Phase 2: Domain Types & Interfaces

- [x] **2.1** Create core domain types
  Create `internal/domain/types.go` with all domain types the system needs.

  **Objective lifecycle:**
  ```go
  type ObjectiveStatus string
  const (
      ObjectiveStatusPlanning  ObjectiveStatus = "planning"
      ObjectiveStatusApproved  ObjectiveStatus = "approved"
      ObjectiveStatusExecuting ObjectiveStatus = "executing"
      ObjectiveStatusReviewing ObjectiveStatus = "reviewing"
      ObjectiveStatusCompleted ObjectiveStatus = "completed"
      ObjectiveStatusFailed    ObjectiveStatus = "failed"
  )

  type Objective struct {
      ID          string          `json:"id"`
      Description string          `json:"description"`
      Status      ObjectiveStatus `json:"status"`
      Blueprint   string          `json:"blueprint"`
      CreatedAt   time.Time       `json:"created_at"`
      UpdatedAt   time.Time       `json:"updated_at"`
  }
  ```

  **Plan and streams:**
  ```go
  type PlanStatus string
  const (
      PlanStatusDraft           PlanStatus = "draft"
      PlanStatusPendingApproval PlanStatus = "pending_approval"
      PlanStatusApproved        PlanStatus = "approved"
      PlanStatusExecuting       PlanStatus = "executing"
      PlanStatusCompleted       PlanStatus = "completed"
      PlanStatusFailed          PlanStatus = "failed"
  )

  type Plan struct {
      ID           string     `json:"id"`
      ObjectiveID  string     `json:"objective_id"`
      Status       PlanStatus `json:"status"`
      QualityGates []string   `json:"quality_gates"`
      CreatedAt    time.Time  `json:"created_at"`
      UpdatedAt    time.Time  `json:"updated_at"`
  }

  type Stream struct {
      ID           string   `json:"id"`
      PlanID       string   `json:"plan_id"`
      Title        string   `json:"title"`
      Description  string   `json:"description"`
      FileScope    []string `json:"file_scope"`
      Dependencies []string `json:"dependencies"`
      Status       string   `json:"status"`
      CreatedAt    time.Time `json:"created_at"`
  }
  ```

  **Agent sessions:**
  ```go
  type AgentRole string
  const (
      AgentRolePlanner AgentRole = "planner"
      AgentRoleLead    AgentRole = "lead"
      AgentRoleWorker  AgentRole = "worker"
      AgentRoleMerger  AgentRole = "merger"
  )

  type AgentSession struct {
      ID          string    `json:"id"`
      ObjectiveID string    `json:"objective_id"`
      StreamID    string    `json:"stream_id"`
      Role        AgentRole `json:"role"`
      SandboxID   string    `json:"sandbox_id"`
      Status      string    `json:"status"`
      CreatedAt   time.Time `json:"created_at"`
      UpdatedAt   time.Time `json:"updated_at"`
  }
  ```

  **Mail messages:**
  ```go
  type MailMessage struct {
      ID        int64     `json:"id"`
      From      string    `json:"from"`
      To        string    `json:"to"`
      Type      string    `json:"type"`
      Payload   string    `json:"payload"`
      Objective string    `json:"objective"`
      Stream    string    `json:"stream"`
      Read      bool      `json:"read"`
      CreatedAt time.Time `json:"created_at"`
  }
  ```

  **Events:**
  ```go
  type EventType string
  const (
      EventObjectiveCreated EventType = "objective.created"
      EventObjectiveUpdated EventType = "objective.updated"
      EventPlanCreated      EventType = "plan.created"
      EventPlanApproved     EventType = "plan.approved"
      EventAgentSpawned     EventType = "agent.spawned"
      EventAgentCompleted   EventType = "agent.completed"
      EventAgentFailed      EventType = "agent.failed"
      EventMailSent         EventType = "mail.sent"
      EventMergeQueued      EventType = "merge.queued"
      EventMergeCompleted   EventType = "merge.completed"
      EventMergeFailed      EventType = "merge.failed"
      EventEscalation       EventType = "escalation"
  )

  type Event struct {
      ID        int64     `json:"id"`
      Type      EventType `json:"type"`
      Objective string    `json:"objective"`
      Stream    string    `json:"stream"`
      Agent     string    `json:"agent"`
      Payload   string    `json:"payload"`
      CreatedAt time.Time `json:"created_at"`
  }
  ```

  Import `time` package. All types need JSON tags for API serialization.
  File: `internal/domain/types.go`

- [x] **2.2** Create sandbox provider interface
  Create `internal/sandbox/provider.go` with the SandboxProvider and Sandbox interfaces.
  Create `internal/sandbox/types.go` with all supporting types.

  **provider.go:**
  ```go
  package sandbox

  import "context"

  type SandboxProvider interface {
      Create(ctx context.Context, opts CreateOpts) (Sandbox, error)
      Get(ctx context.Context, id string) (Sandbox, error)
      List(ctx context.Context, labels map[string]string) ([]Sandbox, error)
      Delete(ctx context.Context, id string) error
  }

  type Sandbox interface {
      ID() string
      Status() SandboxStatus
      Exec(ctx context.Context, cmd string, opts ExecOpts) (ExecResult, error)
      Upload(ctx context.Context, content []byte, path string) error
      Download(ctx context.Context, path string) ([]byte, error)
      Stop(ctx context.Context) error
      Start(ctx context.Context) error
  }
  ```

  **types.go:** `SandboxStatus` (string type with constants: Running, Stopped, Creating, Error), `CreateOpts` (Name, Labels, Snapshot, Resources, EnvVars, AutoStop, AutoDelete, Ephemeral), `ResourceSpec` (CPU, Memory, Disk int), `ExecOpts` (WorkDir, Env, Timeout), `ExecResult` (ExitCode int, Stdout, Stderr string), `VolumeMount` (VolumeID, MountPath, Subpath string).

  These are interfaces only — no implementations in this phase.
  Files: `internal/sandbox/provider.go`, `internal/sandbox/types.go`

- [x] **2.3** Create agent runtime interface
  Create `internal/runtime/runtime.go` with:
  ```go
  package runtime

  import "context"

  type AgentRuntime interface {
      Spawn(ctx context.Context, sandbox sandbox.Sandbox, opts AgentOpts) (AgentProcess, error)
      Name() string
      SupportsRPC() bool
      SupportsHooks() bool
  }

  type AgentProcess interface {
      Send(ctx context.Context, msg AgentMessage) error
      Output() <-chan AgentEvent
      Wait() (AgentResult, error)
      Kill() error
  }

  type AgentOpts struct {
      Role    string
      Overlay string
      Tools   []string
      Rules   []string
      Model   string
      EnvVars map[string]string
      WorkDir string
  }

  type AgentMessage struct {
      Type    string // "prompt", "steer"
      Content string
  }

  type AgentEvent struct {
      Type    string // "output", "tool_call", "error"
      Content string
  }

  type AgentResult struct {
      Success bool
      Summary string
      Error   string
  }
  ```

  Import the sandbox package for the Sandbox interface reference: `github.com/syndg/deck/internal/sandbox`.
  File: `internal/runtime/runtime.go`

- [x] **2.4** Create configuration types and YAML loader
  Create `internal/config/config.go` with:

  ```go
  type Config struct {
      Daemon   DaemonConfig   `yaml:"daemon"`
      Sandbox  SandboxConfig  `yaml:"sandbox"`
      Agents   AgentsConfig   `yaml:"agents"`
      Planning PlanningConfig `yaml:"planning"`
      Watchdog WatchdogConfig `yaml:"watchdog"`
      Tools    ToolsConfig    `yaml:"tools"`
      QualityGates []string   `yaml:"quality_gates"`
  }

  type DaemonConfig struct {
      Listen  string `yaml:"listen"`
      DataDir string `yaml:"data_dir"`
  }

  type SandboxConfig struct {
      Provider         string       `yaml:"provider"`
      DefaultResources ResourceConfig `yaml:"default_resources"`
      AutoStopMinutes  int          `yaml:"auto_stop_interval"`
      AutoDeleteMinutes int         `yaml:"auto_delete_interval"`
  }

  type ResourceConfig struct {
      CPU    int `yaml:"cpu"`
      Memory int `yaml:"memory"`
      Disk   int `yaml:"disk"`
  }

  type AgentsConfig struct {
      Runtime           string `yaml:"runtime"`
      MaxConcurrent     int    `yaml:"max_concurrent"`
      MaxDepth          int    `yaml:"max_depth"`
      StaggerDelayMs    int    `yaml:"stagger_delay_ms"`
      IdleTimeoutMinutes int   `yaml:"idle_timeout_minutes"`
  }

  type PlanningConfig struct {
      DefaultMode string `yaml:"default_mode"`
      Model       string `yaml:"model"`
  }

  type WatchdogConfig struct {
      CheckIntervalSeconds int `yaml:"check_interval_seconds"`
      NudgeAfterMinutes    int `yaml:"nudge_after_minutes"`
      EscalateAfterNudges  int `yaml:"escalate_after_nudges"`
  }

  type ToolsConfig struct {
      MaxPerAgent    int      `yaml:"max_per_agent"`
      AlwaysInclude  []string `yaml:"always_include"`
      AlwaysExclude  []string `yaml:"always_exclude"`
  }
  ```

  Functions:
  - `Load(path string) (*Config, error)` — reads YAML file at path using `gopkg.in/yaml.v3`, returns parsed Config. If file not found, return `Default()`.
  - `Default() *Config` — returns sensible defaults: listen `"0.0.0.0:9800"`, data_dir `"~/.deck/data"`, provider `"daytona"`, max_concurrent `8`, max_depth `2`, etc.
  - `(c *Config) ExpandPaths()` — expands `~` in DataDir to actual home directory using `os.UserHomeDir()`.

  Use `os.ReadFile` + `yaml.Unmarshal`. Import `gopkg.in/yaml.v3` and `os`.
  File: `internal/config/config.go`

---

## Phase 3: Data Layer

- [x] **3.1** Create database manager
  Create `internal/db/db.go` with:

  ```go
  package db

  import (
      "database/sql"
      "fmt"
      "os"
      "path/filepath"
      _ "modernc.org/sqlite"
  )

  type DB struct {
      conn *sql.DB
  }

  func Open(dataDir string) (*DB, error)
  func (d *DB) Close() error
  func (d *DB) Conn() *sql.DB
  func (d *DB) Migrate() error
  ```

  `Open` must:
  1. Create dataDir if it doesn't exist (`os.MkdirAll`)
  2. Open SQLite at `{dataDir}/deck.db` using driver name `"sqlite"`
  3. Set pragmas: `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`
  4. Return `&DB{conn: sqlDB}`

  `Migrate` calls the migration function from task 3.2.
  `Conn` returns the underlying `*sql.DB` for stores to use.

  Register the sqlite driver import with blank identifier: `_ "modernc.org/sqlite"`.
  The driver name for modernc sqlite is `"sqlite"`.
  File: `internal/db/db.go`

- [x] **3.2** Create database schema migrations
  Create `internal/db/migrations.go` with migration SQL as a Go const string.

  ```go
  const migrationSQL = `
  CREATE TABLE IF NOT EXISTS objectives (
      id TEXT PRIMARY KEY,
      description TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'planning',
      blueprint TEXT NOT NULL DEFAULT '',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS plans (
      id TEXT PRIMARY KEY,
      objective_id TEXT NOT NULL REFERENCES objectives(id),
      status TEXT NOT NULL DEFAULT 'draft',
      quality_gates TEXT NOT NULL DEFAULT '[]',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS streams (
      id TEXT PRIMARY KEY,
      plan_id TEXT NOT NULL REFERENCES plans(id),
      title TEXT NOT NULL,
      description TEXT NOT NULL DEFAULT '',
      file_scope TEXT NOT NULL DEFAULT '[]',
      dependencies TEXT NOT NULL DEFAULT '[]',
      status TEXT NOT NULL DEFAULT 'pending',
      created_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS agent_sessions (
      id TEXT PRIMARY KEY,
      objective_id TEXT NOT NULL,
      stream_id TEXT NOT NULL DEFAULT '',
      role TEXT NOT NULL,
      sandbox_id TEXT NOT NULL DEFAULT '',
      status TEXT NOT NULL DEFAULT 'pending',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS mail (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      from_agent TEXT NOT NULL,
      to_agent TEXT NOT NULL,
      type TEXT NOT NULL,
      payload TEXT NOT NULL,
      objective TEXT NOT NULL,
      stream TEXT NOT NULL DEFAULT '',
      read INTEGER NOT NULL DEFAULT 0,
      created_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS events (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      type TEXT NOT NULL,
      objective TEXT NOT NULL DEFAULT '',
      stream TEXT NOT NULL DEFAULT '',
      agent TEXT NOT NULL DEFAULT '',
      payload TEXT NOT NULL DEFAULT '{}',
      created_at INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS merge_queue (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      stream_id TEXT NOT NULL,
      branch TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'pending',
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL
  );

  CREATE INDEX IF NOT EXISTS idx_mail_to_unread ON mail(to_agent, read, created_at);
  CREATE INDEX IF NOT EXISTS idx_events_type ON events(type, created_at);
  CREATE INDEX IF NOT EXISTS idx_events_objective ON events(objective, created_at);
  `
  ```

  Export a function `RunMigrations(db *sql.DB) error` that executes `migrationSQL` via `db.ExecContext`.
  Wire `DB.Migrate()` in db.go to call `RunMigrations(d.conn)`.
  File: `internal/db/migrations.go`

- [x] **3.3** Create objectives and agents stores
  Create `internal/db/objectives.go`:
  ```go
  type ObjectiveStore struct { db *sql.DB }
  func NewObjectiveStore(db *sql.DB) *ObjectiveStore
  func (s *ObjectiveStore) Create(ctx context.Context, obj *domain.Objective) error
  func (s *ObjectiveStore) Get(ctx context.Context, id string) (*domain.Objective, error)
  func (s *ObjectiveStore) List(ctx context.Context) ([]domain.Objective, error)
  func (s *ObjectiveStore) UpdateStatus(ctx context.Context, id string, status domain.ObjectiveStatus) error
  ```
  - `Create`: if `obj.ID` is empty, generate UUID via `uuid.New().String()`. Store `CreatedAt`/`UpdatedAt` as Unix seconds (`time.Now().Unix()`). Set status to `"planning"` if empty.
  - `Get`: query by ID, scan into `Objective`, convert Unix seconds back to `time.Unix(ts, 0)`. Return `sql.ErrNoRows` wrapped if not found.
  - `List`: `SELECT * FROM objectives ORDER BY created_at DESC`.
  - `UpdateStatus`: `UPDATE objectives SET status=?, updated_at=? WHERE id=?`.

  Create `internal/db/agents.go`:
  ```go
  type AgentStore struct { db *sql.DB }
  func NewAgentStore(db *sql.DB) *AgentStore
  func (s *AgentStore) Create(ctx context.Context, session *domain.AgentSession) error
  func (s *AgentStore) Get(ctx context.Context, id string) (*domain.AgentSession, error)
  func (s *AgentStore) ListByObjective(ctx context.Context, objectiveID string) ([]domain.AgentSession, error)
  func (s *AgentStore) UpdateStatus(ctx context.Context, id string, status string) error
  ```
  Same patterns as ObjectiveStore. Generate UUID if ID empty. Store times as Unix seconds.

  Import `github.com/syndg/deck/internal/domain` and `github.com/google/uuid`.
  Files: `internal/db/objectives.go`, `internal/db/agents.go`

- [x] **3.4** Create mail and events stores
  Create `internal/db/mail.go`:
  ```go
  type MailStore struct { db *sql.DB }
  func NewMailStore(db *sql.DB) *MailStore
  func (s *MailStore) Send(ctx context.Context, msg *domain.MailMessage) error
  func (s *MailStore) GetUnread(ctx context.Context, agentName string) ([]domain.MailMessage, error)
  func (s *MailStore) MarkRead(ctx context.Context, id int64) error
  ```
  - `Send`: INSERT into mail table. `created_at` as Unix seconds.
  - `GetUnread`: `SELECT * FROM mail WHERE to_agent=? AND read=0 ORDER BY created_at ASC`.
  - `MarkRead`: `UPDATE mail SET read=1 WHERE id=?`.

  Create `internal/db/events.go`:
  ```go
  type EventStore struct { db *sql.DB }
  func NewEventStore(db *sql.DB) *EventStore
  func (s *EventStore) Insert(ctx context.Context, event *domain.Event) error
  func (s *EventStore) ListByObjective(ctx context.Context, objectiveID string, limit int) ([]domain.Event, error)
  func (s *EventStore) ListRecent(ctx context.Context, limit int) ([]domain.Event, error)
  ```
  - `Insert`: INSERT into events table. `created_at` as Unix seconds.
  - `ListByObjective`: `SELECT * FROM events WHERE objective=? ORDER BY created_at DESC LIMIT ?`.
  - `ListRecent`: `SELECT * FROM events ORDER BY created_at DESC LIMIT ?`.

  Same import pattern as task 3.3.
  Files: `internal/db/mail.go`, `internal/db/events.go`

- [x] **3.5** Create event bus with persistence
  Create `internal/services/events/bus.go`:
  ```go
  package events

  type Subscriber chan domain.Event

  type Bus struct {
      mu          sync.RWMutex
      subscribers map[int]Subscriber
      nextID      int
      logger      *slog.Logger
  }

  func NewBus(logger *slog.Logger) *Bus
  func (b *Bus) Publish(event domain.Event)
  func (b *Bus) Subscribe(buffer int) (Subscriber, func())
  ```
  - `Publish`: RLock, iterate subscribers, non-blocking send (use select with default to drop if full). Log event type via slog.
  - `Subscribe`: Lock, assign incrementing ID, create buffered channel, store in map. Return channel and unsubscribe func that deletes from map.
  - Use `sync.RWMutex` for thread safety.

  Create `internal/services/events/persistent.go`:
  ```go
  type PersistentBus struct {
      *Bus
      store *db.EventStore
  }

  func NewPersistentBus(store *db.EventStore, logger *slog.Logger) *PersistentBus
  func (pb *PersistentBus) Publish(event domain.Event)
  ```
  - `NewPersistentBus`: creates inner Bus, stores reference to EventStore.
  - `Publish`: insert event into store via `pb.store.Insert(context.Background(), &event)`, then call `pb.Bus.Publish(event)` to broadcast to subscribers. Log errors from Insert but don't fail (event bus should be resilient).

  Import `github.com/syndg/deck/internal/domain`, `github.com/syndg/deck/internal/db`, `sync`, `log/slog`.
  Files: `internal/services/events/bus.go`, `internal/services/events/persistent.go`

---

## Phase 4: HTTP Daemon

- [x] **4.1** Create daemon server
  Create `internal/daemon/daemon.go` with:
  ```go
  package daemon

  type Daemon struct {
      cfg       *config.Config
      db        *db.DB
      eventBus  *events.PersistentBus
      objectives *db.ObjectiveStore
      agents    *db.AgentStore
      mail      *db.MailStore
      mux       *http.ServeMux
      server    *http.Server
      startTime time.Time
      logger    *slog.Logger
  }

  func New(cfg *config.Config) (*Daemon, error)
  func (d *Daemon) Start() error
  func (d *Daemon) Shutdown(ctx context.Context) error
  ```
  - `New`: expand config paths, open DB, run migrations, create all stores, create PersistentBus, create `http.ServeMux`, call `d.registerRoutes()`, create `http.Server` with the mux, store start time.
  - `Start`: log "Deck daemon listening on {addr}", call `d.server.ListenAndServe()`. Return `http.ErrServerClosed` as nil (expected on shutdown).
  - `Shutdown`: call `d.server.Shutdown(ctx)`, then `d.db.Close()`.

  Use `log/slog` for structured logging. Create a named logger: `slog.Default().With("component", "daemon")`.

  Import: `net/http`, `log/slog`, `time`, `context`, and internal packages (`config`, `db`, `events`).
  File: `internal/daemon/daemon.go`

- [x] **4.2** Create SSE event stream endpoint
  Create `internal/daemon/sse.go` with:
  ```go
  func (d *Daemon) handleSSE(w http.ResponseWriter, r *http.Request)
  ```
  Implementation:
  1. Set headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
  2. Check that `w` implements `http.Flusher` (cast and check)
  3. Subscribe to event bus: `sub, unsub := d.eventBus.Subscribe(64)`
  4. `defer unsub()`
  5. Loop: `select` on `r.Context().Done()` (client disconnected) or `event := <-sub` (new event)
  6. For each event, marshal to JSON, write `data: {json}\n\n` format, flush
  7. On context done, return

  Register as `GET /events` in registerRoutes.
  File: `internal/daemon/sse.go`

- [x] **4.3** Create objectives REST endpoints
  Create `internal/daemon/routes.go` with:
  ```go
  func (d *Daemon) registerRoutes()
  func (d *Daemon) handleCreateObjective(w http.ResponseWriter, r *http.Request)
  func (d *Daemon) handleGetObjective(w http.ResponseWriter, r *http.Request)
  func (d *Daemon) handleListObjectives(w http.ResponseWriter, r *http.Request)
  ```
  - `registerRoutes`: register all handlers using Go 1.22 pattern syntax:
    ```go
    d.mux.HandleFunc("POST /objectives", d.handleCreateObjective)
    d.mux.HandleFunc("GET /objectives/{id}", d.handleGetObjective)
    d.mux.HandleFunc("GET /objectives", d.handleListObjectives)
    d.mux.HandleFunc("GET /events", d.handleSSE)
    d.mux.HandleFunc("GET /health", d.handleHealth)
    d.mux.HandleFunc("GET /status", d.handleStatus)
    ```
  - `handleCreateObjective`: decode JSON body `{"description": "..."}`, create `domain.Objective`, call `objectives.Create`, publish `EventObjectiveCreated` event, respond 201 with JSON objective.
  - `handleGetObjective`: extract `{id}` from `r.PathValue("id")` (Go 1.22), call `objectives.Get`, respond 200 or 404.
  - `handleListObjectives`: call `objectives.List`, respond 200 with JSON array.

  Add helpers: `writeJSON(w, status, data)` and `writeError(w, status, message)`.
  Use `encoding/json` for marshal/unmarshal.
  File: `internal/daemon/routes.go`

- [x] **4.4** Create health and status endpoints
  Add to `internal/daemon/routes.go`:
  ```go
  func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request)
  func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request)
  ```
  - `handleHealth`: respond 200 with `{"status": "ok"}`.
  - `handleStatus`: build a status response struct:
    ```go
    type StatusResponse struct {
        Status     string         `json:"status"`
        Uptime     string         `json:"uptime"`
        Objectives map[string]int `json:"objectives"`
    }
    ```
    Query objectives, count by status, compute uptime from `d.startTime`. Respond 200 with JSON.
  File: `internal/daemon/routes.go`

- [x] **4.5** Wire daemon entry point
  Rewrite `cmd/daemon/main.go` to:
  1. Parse `--config` flag (default: `~/.config/deck/config.yaml`)
  2. Load config via `config.Load(configPath)` — falls back to defaults if file missing
  3. Call `config.ExpandPaths()` to resolve `~` in paths
  4. Create daemon via `daemon.New(cfg)`
  5. Set up OS signal handling: create channel for `os.Interrupt` and `syscall.SIGTERM`
  6. Start daemon in a goroutine
  7. Wait for signal, then call `daemon.Shutdown` with 10-second timeout context
  8. Log startup and shutdown messages via `slog`

  Import: `flag`, `os`, `os/signal`, `syscall`, `context`, `time`, `log/slog`, and internal packages.
  File: `cmd/daemon/main.go`

---

## Phase 5: CLI Client

- [ ] **5.1** Create cobra CLI root and daemon command
  Rewrite `cmd/deck/main.go` to just call `Execute()`:
  ```go
  package main

  func main() {
      Execute()
  }
  ```

  Create `cmd/deck/root.go` with:
  ```go
  var (
      cfgPath   string
      daemonURL string
  )

  var rootCmd = &cobra.Command{
      Use:   "deck",
      Short: "Deck - agentic workflow orchestrator",
  }

  func Execute() { rootCmd.Execute() }

  func init() {
      rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "~/.config/deck/config.yaml", "config file path")
      rootCmd.PersistentFlags().StringVar(&daemonURL, "daemon-url", "http://localhost:9800", "daemon HTTP address")
  }
  ```

  Add `daemon` subcommand in the same file (or root.go):
  ```go
  var daemonCmd = &cobra.Command{
      Use:   "daemon",
      Short: "Start the Deck daemon",
      RunE: func(cmd *cobra.Command, args []string) error {
          // Same logic as cmd/daemon/main.go:
          // load config, create daemon, signal handling, start
      },
  }
  ```
  Register with `rootCmd.AddCommand(daemonCmd)` in init().

  Import `github.com/spf13/cobra` and internal packages.
  Files: `cmd/deck/main.go`, `cmd/deck/root.go`

- [ ] **5.2** Create HTTP client for daemon communication
  Create `internal/client/client.go` with:
  ```go
  package client

  type Client struct {
      baseURL    string
      httpClient *http.Client
  }

  func New(baseURL string) *Client
  func (c *Client) CreateObjective(ctx context.Context, description string) (*domain.Objective, error)
  func (c *Client) GetObjective(ctx context.Context, id string) (*domain.Objective, error)
  func (c *Client) ListObjectives(ctx context.Context) ([]domain.Objective, error)
  func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error)

  type StatusResponse struct {
      Status     string         `json:"status"`
      Uptime     string         `json:"uptime"`
      Objectives map[string]int `json:"objectives"`
  }
  ```
  - `New`: set baseURL, create `http.Client` with 10s timeout.
  - `CreateObjective`: POST to `/objectives` with JSON body, decode response.
  - `GetObjective`: GET `/objectives/{id}`, decode response.
  - `ListObjectives`: GET `/objectives`, decode response array.
  - `GetStatus`: GET `/status`, decode response.

  Add helper: `(c *Client) do(ctx, method, path, body) (*http.Response, error)` for common request logic.
  Handle non-2xx responses by returning an error with the status code and body.

  Import: `net/http`, `encoding/json`, `bytes`, `fmt`, `context`, `io`, and `github.com/syndg/deck/internal/domain`.
  File: `internal/client/client.go`

- [ ] **5.3** Create `deck status` command
  Create `cmd/deck/status.go` with:
  ```go
  var statusCmd = &cobra.Command{
      Use:   "status",
      Short: "Show daemon status",
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          status, err := c.GetStatus(cmd.Context())
          // handle error: print "Daemon not reachable at {url}" and return
          // print formatted status: uptime, objective counts by status
      },
  }
  ```
  Register with `rootCmd.AddCommand(statusCmd)` in init().
  Format output as a simple table using `fmt.Printf` with alignment.
  Example output:
  ```
  Deck Daemon Status
    URL:      http://localhost:9800
    Uptime:   2h15m
    Objectives:
      planning:   2
      executing:  1
      completed:  5
  ```
  File: `cmd/deck/status.go`

- [ ] **5.4** Create `deck plan` command stub
  Create `cmd/deck/plan.go` with:
  ```go
  var planCmd = &cobra.Command{
      Use:   "plan [description]",
      Short: "Create a new objective",
      Args:  cobra.ExactArgs(1),
      RunE: func(cmd *cobra.Command, args []string) error {
          c := client.New(daemonURL)
          obj, err := c.CreateObjective(cmd.Context(), args[0])
          // handle error
          fmt.Printf("Created objective %s: %s\n", obj.ID, obj.Description)
          return nil
      },
  }
  ```
  Register with `rootCmd.AddCommand(planCmd)` in init().
  This is a stub — full planning with planner agents comes in a later phase.
  File: `cmd/deck/plan.go`

---

## Execution Order

| Step | Task | Phase |
|------|------|-------|
| 1 | 1.1 Initialize Go module and directory structure | 1 |
| 2 | 1.2 Add core dependencies | 1 |
| 3 | 1.3 Create entry point stubs | 1 |
| 4 | 2.1 Create core domain types | 2 |
| 5 | 2.2 Create sandbox provider interface | 2 |
| 6 | 2.3 Create agent runtime interface | 2 |
| 7 | 2.4 Create configuration types and YAML loader | 2 |
| 8 | 3.1 Create database manager | 3 |
| 9 | 3.2 Create database schema migrations | 3 |
| 10 | 3.3 Create objectives and agents stores | 3 |
| 11 | 3.4 Create mail and events stores | 3 |
| 12 | 3.5 Create event bus with persistence | 3 |
| 13 | 4.1 Create daemon server | 4 |
| 14 | 4.2 Create SSE event stream endpoint | 4 |
| 15 | 4.3 Create objectives REST endpoints | 4 |
| 16 | 4.4 Create health and status endpoints | 4 |
| 17 | 4.5 Wire daemon entry point | 4 |
| 18 | 5.1 Create cobra CLI root and daemon command | 5 |
| 19 | 5.2 Create HTTP client for daemon communication | 5 |
| 20 | 5.3 Create deck status command | 5 |
| 21 | 5.4 Create deck plan command stub | 5 |
