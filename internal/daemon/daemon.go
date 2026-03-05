package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/services/events"
)

// Daemon is the main HTTP server that orchestrates all Deck services.
type Daemon struct {
	cfg        *config.Config
	db         *db.DB
	eventBus   *events.PersistentBus
	objectives *db.ObjectiveStore
	agents     *db.AgentStore
	mail       *db.MailStore
	mux        *http.ServeMux
	server     *http.Server
	startTime  time.Time
	logger     *slog.Logger
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
	eventBus := events.NewPersistentBus(eventStore, logger)

	mux := http.NewServeMux()

	d := &Daemon{
		cfg:        cfg,
		db:         database,
		eventBus:   eventBus,
		objectives: objectiveStore,
		agents:     agentStore,
		mail:       mailStore,
		mux:        mux,
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

