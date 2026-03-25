package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/runtime/claudecode"
	"github.com/syndg/tack/internal/runtime/pi"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/sandbox/daytona"
	"github.com/syndg/tack/internal/sandbox/local"
	"github.com/syndg/tack/internal/services/agents"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
	mail "github.com/syndg/tack/internal/services/mail"
	"github.com/syndg/tack/internal/services/merge"
	"github.com/syndg/tack/internal/services/planner"
)

// Daemon is the main HTTP server that orchestrates all Deck services.
type Daemon struct {
	cfg        *config.Config
	db         *db.DB
	eventBus   *events.PersistentBus
	objectives *db.ObjectiveStore
	agents     *db.AgentStore
	mail       *db.MailStore
	executions *db.ExecutionStore
	plans      *db.PlanStore
	streams    *db.StreamStore

	mailBroker      *mail.Broker
	sandboxProvider sandbox.SandboxProvider
	agentRuntime    runtime.AgentRuntime
	spawner         *dispatch.Spawner
	scheduler       *dispatch.Scheduler
	coordinator     dispatch.Orchestrator

	mergeQueueStore *db.MergeQueueStore
	mergeProcessor  *merge.Processor

	ctx    context.Context
	cancel context.CancelFunc

	lifecycleManager *lifecycle.Manager
	planningService  *planner.Service

	blueprintRegistry *blueprint.Registry
	rulesEngine       *rules.Engine
	toolCurator       *tools.Curator
	gateRunner        *gates.Runner

	mux       *http.ServeMux
	server    *http.Server
	startTime time.Time
	logger    *slog.Logger
}

// New creates a new Daemon from the given config. It opens the database,
// runs migrations, initializes all stores and the event bus, and sets up HTTP routes.
func New(cfg *config.Config) (*Daemon, error) {
	cfg.ExpandPaths()

	logger := slog.Default().With("component", "daemon")

	database, err := db.Open(cfg.Daemon.DataDir)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := database.Migrate(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	conn := database.Conn()
	objectiveStore := db.NewObjectiveStore(conn)
	agentStore := db.NewAgentStore(conn)
	mailStore := db.NewMailStore(conn)
	eventStore := db.NewEventStore(conn)
	executionStore := db.NewExecutionStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	eventBus := events.NewPersistentBus(eventStore, logger)

	lifecycleMgr := lifecycle.New(objectiveStore, planStore, streamStore, agentStore, eventBus, logger)
	planningService := planner.New(planStore, streamStore, objectiveStore, agentStore, lifecycleMgr, eventBus, logger, cfg.QualityGates)

	// Initialize blueprint registry and load defaults
	bpRegistry := blueprint.NewRegistry()
	if err := bpRegistry.LoadDefaults(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("loading default blueprints: %w", err)
	}

	// Optionally load user-level blueprints from ~/.config/tack/blueprints/
	// before project-local blueprints so project files take precedence.
	home, _ := os.UserHomeDir()
	if home != "" {
		userBlueprintsDir := filepath.Join(home, ".config", "tack", "blueprints")
		if info, err := os.Stat(userBlueprintsDir); err == nil && info.IsDir() {
			if err := bpRegistry.LoadFromDir(userBlueprintsDir); err != nil {
				logger.Warn("loading user blueprints", "dir", userBlueprintsDir, "error", err)
			}
		}
	}

	// Optionally load project-local blueprints from .tack/blueprints/
	// last so they override both defaults and user-level blueprints.
	projectBlueprintsDir := filepath.Join(".tack", "blueprints")
	if info, err := os.Stat(projectBlueprintsDir); err == nil && info.IsDir() {
		if err := bpRegistry.LoadFromDir(projectBlueprintsDir); err != nil {
			logger.Warn("loading project blueprints", "dir", projectBlueprintsDir, "error", err)
		}
	}

	bpEngine := blueprint.NewEngine(bpRegistry, logger)

	// Initialize rules engine
	rulesEng := rules.NewEngine(logger)

	// Optionally load project-local rules from .tack/rules/
	projectRulesDir := filepath.Join(".tack", "rules")
	if info, err := os.Stat(projectRulesDir); err == nil && info.IsDir() {
		if err := rulesEng.LoadDir(projectRulesDir); err != nil {
			logger.Warn("loading project rules", "dir", projectRulesDir, "error", err)
		}
	}

	// Optionally load user-level rules from ~/.config/tack/rules/
	if home != "" {
		userRulesDir := filepath.Join(home, ".config", "tack", "rules")
		if info, err := os.Stat(userRulesDir); err == nil && info.IsDir() {
			if err := rulesEng.LoadDir(userRulesDir); err != nil {
				logger.Warn("loading user rules", "dir", userRulesDir, "error", err)
			}
		}
	}

	// Initialize tool curator and gate runner
	toolCur := tools.NewCurator(logger)
	gateRun := gates.NewRunner(logger)

	// Get project root for the sandbox provider.
	projectRoot, err := os.Getwd()
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	// Derive daemon URL: prefer external_url (required for remote sandboxes like Daytona).
	var daemonURL string
	if cfg.Daemon.ExternalURL != "" {
		daemonURL = cfg.Daemon.ExternalURL
		logger.Info("using external daemon URL for agent callbacks", "url", daemonURL)
	} else {
		listenAddr := cfg.Daemon.Listen
		if strings.HasPrefix(listenAddr, "0.0.0.0:") {
			daemonURL = "http://127.0.0.1:" + listenAddr[len("0.0.0.0:"):]
		} else {
			daemonURL = "http://" + listenAddr
		}
	}

	// Create mail broker.
	mailBroker := mail.New(mailStore, agentStore, eventBus, logger)

	// Load credentials store early for sandbox provider setup.
	credsPath := filepath.Join(home, ".config", "tack", "credentials.yaml")
	creds, err := credentials.Load(credsPath)
	if err != nil {
		logger.Warn("loading credentials store", "path", credsPath, "error", err)
		creds, _ = credentials.Load("") // empty store fallback
	}

	// Create sandbox provider based on config.
	var sandboxProv sandbox.SandboxProvider
	switch cfg.Sandbox.Provider {
	case "daytona":
		// Resolve Daytona API key: credentials store → config → env var fallback.
		apiKey, _ := creds.SandboxKey("daytona")
		if apiKey == "" {
			apiKey = cfg.Sandbox.Daytona.APIKey
		}
		if apiKey == "" {
			apiKey = os.Getenv("DAYTONA_API_KEY")
		}
		if apiKey == "" {
			// Fall back to local if no API key
			logger.Warn("daytona provider configured but no API key found, falling back to local")
			lp := local.New(projectRoot, localWorktreeDir(cfg), logger)
			lp.SetPostCreate(cfg.Sandbox.PostCreate)
			lp.Rediscover(context.Background())
			sandboxProv = lp
		} else {
			// Derive repo URL from git remote for Daytona bootstrap.
			repoURL := ""
			if gitCmd := exec.Command("git", "remote", "get-url", "origin"); gitCmd != nil {
				gitCmd.Dir = projectRoot
				if out, err := gitCmd.Output(); err == nil {
					repoURL = strings.TrimSpace(string(out))
				}
			}

			dp, err := daytona.New(daytona.Config{
				APIKey:     apiKey,
				APIURL:     cfg.Sandbox.Daytona.APIURL,
				Snapshot:   cfg.Sandbox.Daytona.Snapshot,
				RepoURL:    repoURL,
				PostCreate: cfg.Sandbox.PostCreate,
			}, creds, logger)
			if err != nil {
				_ = database.Close()
				return nil, fmt.Errorf("creating daytona provider: %w", err)
			}
			sandboxProv = dp
		}
	default: // "local"
		lp := local.New(projectRoot, localWorktreeDir(cfg), logger)
		lp.SetPostCreate(cfg.Sandbox.PostCreate)
		lp.Rediscover(context.Background())
		sandboxProv = lp
	}

	// Create agent runtime based on config.
	var agentRuntime runtime.AgentRuntime
	switch cfg.Agents.Runtime {
	case "pi":
		piModel := cfg.Agents.Pi.Model
		if piModel == "" {
			piModel = cfg.Planning.Model
		}
		agentRuntime = pi.New(pi.RuntimeConfig{
			Model:         piModel,
			Provider:      cfg.Agents.Pi.Provider,
			ThinkingLevel: cfg.Agents.Pi.ThinkingLevel,
		}, logger)
	default: // "claude-code"
		agentRuntime = claudecode.New(cfg.Planning.Model, logger)
	}

	// Determine model provider from config.
	modelProvider := cfg.Agents.Pi.Provider
	if modelProvider == "" {
		modelProvider = "anthropic" // default
	}

	// Create spawner.
	spawner := dispatch.NewSpawner(agentStore, agentRuntime, sandboxProv, rulesEng, toolCur, eventBus, creds, modelProvider, logger, daemonURL)

	// Create scheduler.
	scheduler := dispatch.NewScheduler(streamStore, planStore, cfg.Agents.MaxConcurrent, eventBus, logger)

	// Create merge queue store and processor.
	mergeQueueStore := db.NewMergeQueueStore(conn)
	gitMerger := merge.NewGitMerger(logger)
	diffExtractor := merge.NewDiffExtractor(logger)
	mergeProcessor := merge.NewProcessor(
		mergeQueueStore, streamStore, planStore,
		gitMerger, diffExtractor, gateRun, sandboxProv,
		eventBus, cfg.Daemon.BaseBranch, logger,
	)

	// Create step handlers and register deterministic + human types.
	handlers := dispatch.NewHandlers(scheduler, gateRun, lifecycleMgr, mergeProcessor, planStore, streamStore, objectiveStore, executionStore, agentStore, sandboxProv, eventBus, cfg.Daemon.BaseBranch, logger)
	bpEngine.RegisterHandler(blueprint.StepTypeDeterministic, handlers.HandleDeterministic)
	bpEngine.RegisterHandler(blueprint.StepTypeHuman, handlers.HandleHuman)

	// Create activity logger for agent event tracking.
	activityLogDir := filepath.Join(cfg.Daemon.DataDir, "activity")
	activityLogger, err := agents.NewActivityLogger(activityLogDir, logger)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("creating activity logger: %w", err)
	}

	// Create coordinator.
	coordinator, err := dispatch.NewCoordinator(dispatch.Config{
		Engine:         bpEngine,
		Scheduler:      scheduler,
		Spawner:        spawner,
		Lifecycle:      lifecycleMgr,
		MergeEnqueuer:  mergeProcessor,
		PlanCreator:    planningService,
		MailSender:     mailBroker,
		Executions:     executionStore,
		Objectives:     objectiveStore,
		Plans:          planStore,
		Streams:        streamStore,
		EventBus:       eventBus,
		ActivityLogger: activityLogger,
		Timeouts:       cfg.Agents.Timeouts,
		Logger:         logger,
	})
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("creating coordinator: %w", err)
	}

	// Create daemon lifecycle context (cancelled in Shutdown).
	daemonCtx, daemonCancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()

	d := &Daemon{
		cfg:        cfg,
		db:         database,
		eventBus:   eventBus,
		objectives: objectiveStore,
		agents:     agentStore,
		mail:       mailStore,
		executions: executionStore,
		plans:      planStore,
		streams:    streamStore,

		mailBroker:      mailBroker,
		sandboxProvider: sandboxProv,
		agentRuntime:    agentRuntime,
		spawner:         spawner,
		scheduler:       scheduler,
		coordinator:     coordinator,

		mergeQueueStore: mergeQueueStore,
		mergeProcessor:  mergeProcessor,

		lifecycleManager: lifecycleMgr,
		planningService:  planningService,

		blueprintRegistry: bpRegistry,
		rulesEngine:       rulesEng,
		toolCurator:       toolCur,
		gateRunner:        gateRun,

		ctx:    daemonCtx,
		cancel: daemonCancel,

		mux: mux,
		server: &http.Server{
			Addr:    cfg.Daemon.Listen,
			Handler: mux,
		},
		startTime: time.Now(),
		logger:    logger,
	}

	d.registerRoutes()

	return d, nil
}

// Start begins event processing and HTTP serving. It blocks until the server
// is shut down. Returns nil if shutdown was triggered via Shutdown.
func (d *Daemon) Start() error {
	if d.coordinator != nil {
		if err := d.coordinator.Start(d.ctx); err != nil {
			return fmt.Errorf("starting coordinator: %w", err)
		}
	}
	if d.mergeProcessor != nil {
		if err := d.mergeProcessor.Start(d.ctx); err != nil {
			return fmt.Errorf("starting merge processor: %w", err)
		}
	}
	d.logger.Info("Deck daemon listening", "addr", d.cfg.Daemon.Listen)
	err := d.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// localWorktreeDir returns the worktree directory for the local sandbox provider.
func localWorktreeDir(cfg *config.Config) string {
	if cfg.Sandbox.WorktreeDir != "" {
		return cfg.Sandbox.WorktreeDir
	}
	return filepath.Join(os.TempDir(), "tack-worktrees")
}

// Shutdown gracefully shuts down the coordinator, HTTP server, and database.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.logger.Info("shutting down daemon")
	if d.cancel != nil {
		d.cancel()
	}
	if d.mergeProcessor != nil {
		d.mergeProcessor.Stop()
	}
	if d.coordinator != nil {
		d.coordinator.Stop()
	}
	if err := d.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}
	return nil
}
