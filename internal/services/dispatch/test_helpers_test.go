package dispatch

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// dispatchTestEnv extends testEnv with Handlers and shared test infrastructure.
type dispatchTestEnv struct {
	coord      *Coordinator
	handlers   *Handlers
	tracker    *mockTracker
	engine     *blueprint.Engine
	scheduler  *Scheduler
	executions *db.ExecutionStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	agents     *db.AgentStore
	attempts   *db.AttemptStore
	eventBus   *events.PersistentBus
	lifecycle  *lifecycle.Manager
	logger     *slog.Logger
}

// setupDispatchEnv creates a full dispatch test environment with real stores,
// a real blueprint engine, mock tracker, and Handlers. Callers can override
// step handlers after setup.
func setupDispatchEnv(t *testing.T) *dispatchTestEnv {
	t.Helper()

	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.Migrate(); err != nil {
		t.Fatalf("migrating test db: %v", err)
	}
	if err := db.NewProjectStore(database.Conn()).Upsert(context.Background(), &domain.Project{ID: "test-project", Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir() + "/.tack/config.yaml"}); err != nil {
		t.Fatalf("registering test project: %v", err)
	}

	conn := database.Conn()
	executionStore := db.NewExecutionStore(conn)
	objectiveStore := db.NewObjectiveStore(conn)
	planStore := db.NewPlanStore(conn)
	streamStore := db.NewStreamStore(conn)
	agentStore := db.NewAgentStore(conn)
	attemptStore := db.NewAttemptStore(conn)
	eventBus := events.NewPersistentBus(nil, slog.Default())
	logger := slog.Default()

	lm := lifecycle.New(objectiveStore, planStore, streamStore, agentStore, eventBus, nil, logger)

	reg := blueprint.NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		t.Fatalf("loading default blueprints: %v", err)
	}
	engine := blueprint.NewEngine(reg, logger)

	scheduler := NewScheduler(streamStore, planStore, 10, eventBus, logger)
	tracker := newMockTracker()

	c := &Coordinator{
		engine:        engine,
		scheduler:     scheduler,
		spawner:       nil,
		lifecycle:     lm,
		mergeEnqueuer: &stubMergeEnqueuer{},
		planCreator:   &stubPlanCreator{},
		attempts:      attemptStore,
		executions:    executionStore,
		objectives:    objectiveStore,
		plans:         planStore,
		streams:       streamStore,
		eventBus:      eventBus,
		tracker:       tracker,
		logger:        logger,
		activeExecs:   make(map[string]context.CancelFunc),
	}

	// Register default pass-through step handlers.
	engine.RegisterHandler(blueprint.StepTypeAgent, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted, Output: "test done"}, nil
	})
	engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)
	engine.RegisterHandler(blueprint.StepTypeDeterministic, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	})
	engine.RegisterHandler(blueprint.StepTypeHuman, func(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	})

	return &dispatchTestEnv{
		coord:      c,
		tracker:    tracker,
		engine:     engine,
		scheduler:  scheduler,
		executions: executionStore,
		objectives: objectiveStore,
		plans:      planStore,
		streams:    streamStore,
		agents:     agentStore,
		attempts:   attemptStore,
		eventBus:   eventBus,
		lifecycle:  lm,
		logger:     logger,
	}
}

// createTestObjective creates an objective in the given status.
func (e *dispatchTestEnv) createObjective(t *testing.T, id string, status domain.ObjectiveStatus) {
	t.Helper()
	obj := &domain.Objective{
		ID:          id,
		Description: "test objective " + id,
		Status:      status,
		Blueprint:   "build-review",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := e.objectives.Create(context.Background(), obj); err != nil {
		t.Fatalf("creating test objective: %v", err)
	}
}

// createTestPlan creates a plan with the given streams.
func (e *dispatchTestEnv) createPlan(t *testing.T, planID, objectiveID string, streamTitles []string) []*domain.Stream {
	t.Helper()
	plan := &domain.Plan{
		ID:          planID,
		ObjectiveID: objectiveID,
		Status:      domain.PlanStatusExecuting,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := e.plans.Create(context.Background(), plan); err != nil {
		t.Fatalf("creating test plan: %v", err)
	}

	streams := make([]*domain.Stream, len(streamTitles))
	for i, title := range streamTitles {
		s := &domain.Stream{
			ID:        planID + "-stream-" + title,
			PlanID:    planID,
			Title:     title,
			Status:    domain.StreamStatusPending,
			CreatedAt: time.Now(),
		}
		if err := e.streams.Create(context.Background(), s); err != nil {
			t.Fatalf("creating test stream: %v", err)
		}
		streams[i] = s
	}
	return streams
}
