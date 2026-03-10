package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/harness/gates"
	"github.com/syndg/deck/internal/harness/rules"
	"github.com/syndg/deck/internal/harness/tools"
	"github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
	"github.com/syndg/deck/internal/services/planner"
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

	lifecycleManager *lifecycle.Manager
	planningService  *planner.Service

	blueprintRegistry *blueprint.Registry
	blueprintEngine   *blueprint.Engine
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
		database.Close()
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
	planningService := planner.New(planStore, streamStore, objectiveStore, agentStore, lifecycleMgr, eventBus, logger)

	// Initialize blueprint registry and load defaults
	bpRegistry := blueprint.NewRegistry()
	if err := bpRegistry.LoadDefaults(); err != nil {
		database.Close()
		return nil, fmt.Errorf("loading default blueprints: %w", err)
	}

	// Optionally load user-level blueprints from ~/.config/deck/blueprints/
	// before project-local blueprints so project files take precedence.
	home, _ := os.UserHomeDir()
	if home != "" {
		userBlueprintsDir := filepath.Join(home, ".config", "deck", "blueprints")
		if info, err := os.Stat(userBlueprintsDir); err == nil && info.IsDir() {
			if err := bpRegistry.LoadFromDir(userBlueprintsDir); err != nil {
				logger.Warn("loading user blueprints", "dir", userBlueprintsDir, "error", err)
			}
		}
	}

	// Optionally load project-local blueprints from .deck/blueprints/
	// last so they override both defaults and user-level blueprints.
	projectBlueprintsDir := filepath.Join(".deck", "blueprints")
	if info, err := os.Stat(projectBlueprintsDir); err == nil && info.IsDir() {
		if err := bpRegistry.LoadFromDir(projectBlueprintsDir); err != nil {
			logger.Warn("loading project blueprints", "dir", projectBlueprintsDir, "error", err)
		}
	}

	bpEngine := blueprint.NewEngine(bpRegistry, logger)

	// Initialize rules engine
	rulesEng := rules.NewEngine(logger)

	// Optionally load project-local rules from .deck/rules/
	projectRulesDir := filepath.Join(".deck", "rules")
	if info, err := os.Stat(projectRulesDir); err == nil && info.IsDir() {
		if err := rulesEng.LoadDir(projectRulesDir); err != nil {
			logger.Warn("loading project rules", "dir", projectRulesDir, "error", err)
		}
	}

	// Optionally load user-level rules from ~/.config/deck/rules/
	if home != "" {
		userRulesDir := filepath.Join(home, ".config", "deck", "rules")
		if info, err := os.Stat(userRulesDir); err == nil && info.IsDir() {
			if err := rulesEng.LoadDir(userRulesDir); err != nil {
				logger.Warn("loading user rules", "dir", userRulesDir, "error", err)
			}
		}
	}

	// Initialize tool curator and gate runner
	toolCur := tools.NewCurator(logger)
	gateRun := gates.NewRunner(logger)

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

		lifecycleManager: lifecycleMgr,
		planningService:  planningService,

		blueprintRegistry: bpRegistry,
		blueprintEngine:   bpEngine,
		rulesEngine:       rulesEng,
		toolCurator:       toolCur,
		gateRunner:        gateRun,

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

// Start begins listening for HTTP requests. It blocks until the server
// is shut down. Returns nil if shutdown was triggered via Shutdown.
func (d *Daemon) Start() error {
	d.logger.Info("Deck daemon listening", "addr", d.cfg.Daemon.Listen)
	err := d.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the HTTP server and closes the database.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.logger.Info("shutting down daemon")
	if err := d.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}
	return nil
}
