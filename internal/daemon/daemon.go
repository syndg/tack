package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/daemonauth"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/services/events"
	mailservice "github.com/syndg/tack/internal/services/mail"
	"github.com/syndg/tack/internal/services/merge"
	"github.com/syndg/tack/internal/services/runs"
)

// Ensure merge.Processor satisfies the runs.MergeOrchestrator interface.
var _ runs.MergeOrchestrator = (*merge.Processor)(nil)

type Daemon struct {
	cfg *config.Config
	db  *db.DB

	projectStore    *db.ProjectStore
	objectives      *db.ObjectiveStore
	agents          *db.AgentStore
	mail            *db.MailStore
	events          *db.EventStore
	executions      *db.ExecutionStore
	plans           *db.PlanStore
	streams         *db.StreamStore
	runStore        *db.RunStore
	attempts        *db.AttemptStore
	mergeQueue      *db.MergeQueueStore
	mergeQueueStore *db.MergeQueueStore

	eventBus      *events.PersistentBus
	mailBroker    *mailservice.Broker
	projectCtxs   *ProjectContextManager
	creds         *credentials.Store
	observability *observability.Recorder
	authToken     string

	ctx    context.Context
	cancel context.CancelFunc

	mux       *http.ServeMux
	server    *http.Server
	startTime time.Time
	logger    *slog.Logger
}

// New creates the machine-wide multi-project daemon.
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
	projectStore := db.NewProjectStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	agentStore := db.NewAgentStore(conn)
	mailStore := db.NewMailStore(conn)
	eventStore := db.NewEventStore(conn)
	executionStore := db.NewExecutionStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	runStore := db.NewRunStore(conn)
	attemptStore := db.NewAttemptStore(conn)
	mergeQueueStore := db.NewMergeQueueStore(conn)
	eventBus := events.NewPersistentBus(eventStore, logger)

	home, _ := os.UserHomeDir()
	credsPath := filepath.Join(home, ".config", "tack", "credentials.yaml")
	creds, err := credentials.Load(credsPath)
	if err != nil {
		logger.Warn("loading credentials store", "path", credsPath, "error", err)
		creds, _ = credentials.Load("")
	}

	activityDir := filepath.Join(cfg.Daemon.DataDir, "activity")
	recorder, err := observability.New(activityDir, eventBus, logger)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("creating observability recorder: %w", err)
	}
	mailBroker := mailservice.New(mailStore, agentStore, eventBus, recorder, logger)

	ctx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	authToken, err := daemonauth.LoadOrCreate()
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("loading daemon auth token: %w", err)
	}
	daemonURL := deriveDaemonURL(cfg, logger)
	projectCtxs := newProjectContextManager(
		cfg,
		projectStore,
		objectiveStore,
		agentStore,
		mailStore,
		executionStore,
		planStore,
		streamStore,
		runStore,
		attemptStore,
		mergeQueueStore,
		eventBus,
		mailBroker,
		creds,
		recorder,
		daemonURL,
		authToken,
		logger,
	)

	d := &Daemon{
		cfg:             cfg,
		db:              database,
		projectStore:    projectStore,
		objectives:      objectiveStore,
		agents:          agentStore,
		mail:            mailStore,
		events:          eventStore,
		executions:      executionStore,
		plans:           planStore,
		streams:         streamStore,
		runStore:        runStore,
		attempts:        attemptStore,
		mergeQueue:      mergeQueueStore,
		mergeQueueStore: mergeQueueStore,
		eventBus:        eventBus,
		mailBroker:      mailBroker,
		projectCtxs:     projectCtxs,
		creds:           creds,
		observability:   recorder,
		authToken:       authToken,
		ctx:             ctx,
		cancel:          cancel,
		mux:             mux,
		server: &http.Server{
			Addr:    cfg.Daemon.Listen,
			Handler: nil,
		},
		startTime: time.Now(),
		logger:    logger,
	}

	d.registerRoutes()
	d.server.Handler = d.authMiddleware(mux)
	return d, nil
}

func (d *Daemon) Start() error {
	if err := d.projectCtxs.StartAll(d.ctx); err != nil {
		return err
	}
	d.logger.Info("Tack daemon listening", "addr", d.cfg.Daemon.Listen)
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

func deriveDaemonURL(cfg *config.Config, logger *slog.Logger) string {
	if cfg.Daemon.ExternalURL != "" {
		logger.Info("using external daemon URL for agent callbacks", "url", cfg.Daemon.ExternalURL)
		return cfg.Daemon.ExternalURL
	}
	listenAddr := cfg.Daemon.Listen
	if strings.HasPrefix(listenAddr, "0.0.0.0:") {
		return "http://127.0.0.1:" + listenAddr[len("0.0.0.0:"):]
	}
	return "http://" + listenAddr
}

func daemonURLIsLoopback(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

func (d *Daemon) Shutdown(ctx context.Context) error {
	d.logger.Info("shutting down daemon")
	if d.eventBus != nil {
		d.eventBus.Shutdown()
	}
	if d.cancel != nil {
		d.cancel()
	}
	if d.projectCtxs != nil {
		d.projectCtxs.Stop()
	}
	if d.observability != nil {
		d.observability.Close()
	}
	if err := d.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}
	return nil
}
