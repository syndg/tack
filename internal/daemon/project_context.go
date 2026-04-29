package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/runtime/claudecode"
	"github.com/syndg/tack/internal/runtime/pi"
	"github.com/syndg/tack/internal/runtimeauth"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/sandbox/daytona"
	"github.com/syndg/tack/internal/sandbox/local"
	"github.com/syndg/tack/internal/services/cleanup"
	"github.com/syndg/tack/internal/services/discovery"
	"github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
	mailservice "github.com/syndg/tack/internal/services/mail"
	"github.com/syndg/tack/internal/services/merge"
	"github.com/syndg/tack/internal/services/planner"
	"github.com/syndg/tack/internal/services/runs"
	"gopkg.in/yaml.v3"
)

type ProjectContext struct {
	Project           domain.Project
	Config            *config.Config
	BlueprintRegistry *blueprint.Registry
	RulesEngine       *rules.Engine
	SandboxProvider   sandbox.SandboxProvider
	Lifecycle         *lifecycle.Manager
	DiscoveryService  *discovery.Service
	PlanningService   *planner.Service
	MergeProcessor    *merge.Processor
	RunsService       *runs.Service
	BranchJanitor     *cleanup.Janitor

	started bool
}

type ProjectContextManager struct {
	baseConfig     *config.Config
	userConfigPath string
	daemonURL      string
	daemonToken    string
	logger         *slog.Logger

	projectStore *db.ProjectStore
	objectives   *db.ObjectiveStore
	agents       *db.AgentStore
	mail         *db.MailStore
	executions   *db.ExecutionStore
	plans        *db.PlanStore
	dossiers     *db.DossierStore
	streams      *db.StreamStore
	runs         *db.RunStore
	attempts     *db.AttemptStore
	insights     *db.ObjectiveInsightStore
	mergeQueue   *db.MergeQueueStore
	eventBus     *events.PersistentBus
	mailBroker   *mailservice.Broker
	creds        *credentials.Store
	obs          *observability.Recorder

	mu        sync.Mutex
	contexts  map[string]*ProjectContext
	started   bool
	daemonCtx context.Context
}

func newProjectContextManager(
	baseCfg *config.Config,
	projectStore *db.ProjectStore,
	objectiveStore *db.ObjectiveStore,
	agentStore *db.AgentStore,
	mailStore *db.MailStore,
	executionStore *db.ExecutionStore,
	planStore *db.PlanStore,
	dossierStore *db.DossierStore,
	streamStore *db.StreamStore,
	runStore *db.RunStore,
	attemptStore *db.AttemptStore,
	insightStore *db.ObjectiveInsightStore,
	mergeQueueStore *db.MergeQueueStore,
	eventBus *events.PersistentBus,
	mailBroker *mailservice.Broker,
	creds *credentials.Store,
	obs *observability.Recorder,
	daemonURL string,
	daemonToken string,
	logger *slog.Logger,
) *ProjectContextManager {
	userCfg := config.UserConfigPath
	if v := os.Getenv("TACK_USER_CONFIG_PATH"); v != "" {
		userCfg = v
	}
	return &ProjectContextManager{
		baseConfig:     baseCfg,
		userConfigPath: userCfg,
		daemonURL:      daemonURL,
		daemonToken:    daemonToken,
		logger:         logger,
		projectStore:   projectStore,
		objectives:     objectiveStore,
		agents:         agentStore,
		mail:           mailStore,
		executions:     executionStore,
		plans:          planStore,
		dossiers:       dossierStore,
		streams:        streamStore,
		runs:           runStore,
		attempts:       attemptStore,
		insights:       insightStore,
		mergeQueue:     mergeQueueStore,
		eventBus:       eventBus,
		mailBroker:     mailBroker,
		creds:          creds,
		obs:            obs,
		contexts:       make(map[string]*ProjectContext),
	}
}

func (m *ProjectContextManager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.daemonCtx = ctx
	m.mu.Unlock()

	projects, err := m.projectStore.List(ctx)
	if err != nil {
		return fmt.Errorf("listing registered projects: %w", err)
	}
	for _, project := range projects {
		if _, err := m.Get(ctx, project.ID); err != nil {
			m.logger.Warn("starting project context", "project_id", project.ID, "root", project.RootPath, "error", err)
		}
	}
	return nil
}

func (m *ProjectContextManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ctx := range m.contexts {
		if ctx.RunsService != nil {
			ctx.RunsService.Stop()
		}
		delete(m.contexts, id)
	}
}

func (m *ProjectContextManager) Invalidate(projectID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx, ok := m.contexts[projectID]; ok {
		if ctx.RunsService != nil {
			ctx.RunsService.Stop()
		}
		delete(m.contexts, projectID)
	}
}

func (m *ProjectContextManager) Get(ctx context.Context, projectID string) (*ProjectContext, error) {
	m.mu.Lock()
	if existing, ok := m.contexts[projectID]; ok {
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	project, err := m.projectStore.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	loaded, err := m.load(project)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.contexts[projectID]; ok {
		if loaded.RunsService != nil {
			loaded.RunsService.Stop()
		}
		return existing, nil
	}
	if m.started {
		if err := m.startLocked(loaded); err != nil {
			return nil, err
		}
	}
	m.contexts[projectID] = loaded
	return loaded, nil
}

func (m *ProjectContextManager) startLocked(ctx *ProjectContext) error {
	if ctx.started {
		return nil
	}
	if ctx.RunsService != nil {
		if err := ctx.RunsService.Run(m.daemonCtx); err != nil {
			return fmt.Errorf("starting runs service: %w", err)
		}
	}
	if ctx.BranchJanitor != nil {
		ctx.BranchJanitor.Start(m.daemonCtx)
	}
	ctx.started = true
	return nil
}

func (m *ProjectContextManager) load(project *domain.Project) (*ProjectContext, error) {
	cfg := *m.baseConfig
	cfg.QualityGates = append([]string(nil), m.baseConfig.QualityGates...)
	if project.ConfigPath != "" {
		data, err := os.ReadFile(project.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("reading config for project %s: %w", project.ID, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("loading config for project %s: %w", project.ID, err)
		}
	}
	cfg.ExpandPaths()
	binding := cfg.EffectiveRuntimeAuth()
	adapter, err := runtimeauth.New(binding.Runtime)
	if err != nil {
		return nil, err
	}
	probe, err := adapter.Probe(context.Background())
	if err != nil {
		m.logger.Warn("runtime auth probe failed", "runtime", binding.Runtime, "project_id", project.ID, "error", err)
	}
	if binding.Mode == runtimeauth.ModeNative && binding.Method == "" {
		binding.Method = probe.NativeMethods[binding.Provider]
	}
	if err := runtimeauth.ValidateBinding(adapter, binding, cfg.Sandbox.Provider); err != nil {
		return nil, err
	}
	if binding.Mode == runtimeauth.ModeNative && err == nil && !slices.Contains(probe.NativeProviders, binding.Provider) {
		return nil, fmt.Errorf("native auth for provider %s was not detected for runtime %s", binding.Provider, binding.Runtime)
	}

	bpRegistry := blueprint.NewRegistry()
	if err := bpRegistry.LoadDefaults(); err != nil {
		return nil, fmt.Errorf("loading default blueprints: %w", err)
	}
	if userDir := userBlueprintsDir(); userDir != "" {
		if info, err := os.Stat(userDir); err == nil && info.IsDir() {
			if err := bpRegistry.LoadFromDir(userDir); err != nil {
				m.logger.Warn("loading user blueprints", "dir", userDir, "error", err)
			}
		}
	}
	projectBlueprintsDir := filepath.Join(project.RootPath, config.ProjectConfigDir, "blueprints")
	if info, err := os.Stat(projectBlueprintsDir); err == nil && info.IsDir() {
		if err := bpRegistry.LoadFromDir(projectBlueprintsDir); err != nil {
			m.logger.Warn("loading project blueprints", "project_id", project.ID, "dir", projectBlueprintsDir, "error", err)
		}
	}
	bpEngine := blueprint.NewEngine(bpRegistry, m.logger)

	rulesEng := rules.NewEngine(m.logger)
	if userDir := userRulesDir(); userDir != "" {
		if info, err := os.Stat(userDir); err == nil && info.IsDir() {
			if err := rulesEng.LoadDir(userDir); err != nil {
				m.logger.Warn("loading user rules", "dir", userDir, "error", err)
			}
		}
	}
	projectRulesDir := filepath.Join(project.RootPath, config.ProjectConfigDir, "rules")
	if info, err := os.Stat(projectRulesDir); err == nil && info.IsDir() {
		if err := rulesEng.LoadDir(projectRulesDir); err != nil {
			m.logger.Warn("loading project rules", "project_id", project.ID, "dir", projectRulesDir, "error", err)
		}
	}

	sandboxProv, err := m.newSandboxProvider(project, &cfg)
	if err != nil {
		return nil, err
	}
	mergeProcessor := merge.NewProcessor(
		project.ID,
		m.mergeQueue,
		m.insights,
		m.streams,
		m.plans,
		m.objectives,
		bpEngine,
		m.attempts,
		merge.NewGitMerger(m.logger),
		merge.NewDiffExtractor(m.logger),
		gates.NewRunner(m.logger),
		sandboxProv,
		m.eventBus,
		m.obs,
		cfg.Benchmark,
		cfg.Daemon.BaseBranch,
		m.logger,
	)
	lifecycleMgr := lifecycle.New(m.objectives, m.plans, m.streams, m.agents, m.eventBus, m.obs, m.logger)
	planningService := planner.New(m.plans, m.streams, m.dossiers, m.objectives, m.agents, lifecycleMgr, m.eventBus, m.obs, m.logger, cfg.QualityGates)
	discoveryService := discovery.New(project.RootPath, m.objectives, m.dossiers, rulesEng, bpRegistry, m.logger)
	planningService.BindInsightStore(m.insights)
	discoveryService.BindInsightStore(m.insights)

	toolCurator := tools.NewCurator(m.logger)
	agentRuntime := newAgentRuntime(&cfg, m.logger)
	runsService, err := runs.New(runs.Config{
		ProjectID:           project.ID,
		ProjectRoot:         project.RootPath,
		Engine:              bpEngine,
		AgentRuntime:        agentRuntime,
		SandboxProvider:     sandboxProv,
		RulesEngine:         rulesEng,
		ToolCurator:         toolCurator,
		Credentials:         m.creds,
		RuntimeAuth:         binding,
		SandboxProviderName: cfg.Sandbox.Provider,
		DaemonExternalURL:   cfg.Daemon.ExternalURL,
		AgentModel:          cfg.EffectiveAgentModel(),
		PlannerModel:        cfg.EffectivePlannerModel(),
		DeterministicModel:  cfg.EffectiveSmallTaskModel(),
		DaemonURL:           m.daemonURL,
		DaemonToken:         m.daemonToken,
		Lifecycle:           lifecycleMgr,
		Discovery:           discoveryService,
		MergeProcessor:      mergeProcessor,
		PlanCreator:         planningService,
		MailSender:          m.mailBroker,
		GateRunner:          gates.NewRunner(m.logger),
		Observability:       m.obs,
		Timeouts:            cfg.Agents.Timeouts,
		MaxConcurrent:       cfg.Agents.MaxConcurrent,
		BaseBranch:          cfg.Daemon.BaseBranch,
		GitAuthorName:       cfg.Git.AuthorName,
		GitAuthorEmail:      cfg.Git.AuthorEmail,
		Runs:                m.runs,
		Attempts:            m.attempts,
		Insights:            m.insights,
		Objectives:          m.objectives,
		Plans:               m.plans,
		Streams:             m.streams,
		Executions:          m.executions,
		Agents:              m.agents,
		EventBus:            m.eventBus,
		Logger:              m.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating runs service for project %s: %w", project.ID, err)
	}
	planningService.BindRunController(m.runs, runsService)

	return &ProjectContext{
		Project:           *project,
		Config:            &cfg,
		BlueprintRegistry: bpRegistry,
		RulesEngine:       rulesEng,
		SandboxProvider:   sandboxProv,
		Lifecycle:         lifecycleMgr,
		DiscoveryService:  discoveryService,
		PlanningService:   planningService,
		MergeProcessor:    mergeProcessor,
		RunsService:       runsService,
		BranchJanitor:     cleanup.NewJanitor(m.objectives, project.ID, project.RootPath, m.logger),
	}, nil
}

func (m *ProjectContextManager) newSandboxProvider(project *domain.Project, cfg *config.Config) (sandbox.SandboxProvider, error) {
	switch cfg.Sandbox.Provider {
	case "daytona":
		apiKey, _ := m.creds.SandboxKey("daytona")
		if apiKey == "" {
			apiKey = cfg.Sandbox.Daytona.APIKey
		}
		if apiKey == "" {
			m.logger.Warn("daytona provider configured but no API key found, falling back to local", "project_id", project.ID)
			return newLocalProvider(project.RootPath, cfg, m.logger), nil
		}
		if daemonURLIsLoopback(m.daemonURL) {
			return nil, fmt.Errorf("daytona sandboxes require daemon.external_url to be reachable from the sandbox; current callback URL %q resolves to loopback", m.daemonURL)
		}
		repoURL := ""
		gitCmd := exec.Command("git", "remote", "get-url", "origin")
		gitCmd.Dir = project.RootPath
		if out, err := gitCmd.Output(); err == nil {
			repoURL = strings.TrimSpace(string(out))
		}
		dp, err := daytona.New(daytona.Config{
			APIKey:       apiKey,
			APIURL:       cfg.Sandbox.Daytona.APIURL,
			Snapshot:     cfg.Sandbox.Daytona.Snapshot,
			RepoURL:      repoURL,
			ProjectSetup: effectiveProjectSetup(cfg),
		}, m.creds, m.logger)
		if err != nil {
			return nil, fmt.Errorf("creating daytona provider for project %s: %w", project.ID, err)
		}
		return dp, nil
	default:
		return newLocalProvider(project.RootPath, cfg, m.logger), nil
	}
}

func newLocalProvider(projectRoot string, cfg *config.Config, logger *slog.Logger) sandbox.SandboxProvider {
	lp := local.New(projectRoot, localWorktreeDir(cfg), logger)
	lp.SetProjectSetup(effectiveProjectSetup(cfg))
	lp.Rediscover(context.Background())
	return lp
}

func newAgentRuntime(cfg *config.Config, logger *slog.Logger) runtime.AgentRuntime {
	switch cfg.Agents.Runtime {
	case "pi":
		binding := cfg.EffectiveRuntimeAuth()
		return pi.New(pi.RuntimeConfig{
			Model:         cfg.EffectiveAgentModel(),
			Provider:      binding.Provider,
			ThinkingLevel: cfg.Agents.Pi.ThinkingLevel,
		}, logger)
	default:
		return claudecode.New(cfg.EffectiveAgentModel(), logger)
	}
}

func userBlueprintsDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "tack", "blueprints")
}

func userRulesDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "tack", "rules")
}

func effectiveProjectSetup(cfg *config.Config) sandbox.ProjectSetup {
	setup := cfg.EffectiveProjectSetup()
	commands := make([]string, 0, len(setup.Commands))
	for _, cmd := range setup.Commands {
		normalized := strings.TrimSpace(cmd)
		if cfg.Agents.Runtime == "pi" && strings.Contains(normalized, "@mariozechner/pi-coding-agent") {
			continue
		}
		commands = append(commands, cmd)
	}
	return sandbox.ProjectSetup{Commands: commands, Verify: append([]string(nil), setup.Verify...)}
}
