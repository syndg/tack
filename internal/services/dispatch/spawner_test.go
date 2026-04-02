package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
	events "github.com/syndg/tack/internal/services/events"
)

// mockRuntime records the opts passed to the last Spawn call.
type mockRuntime struct {
	lastOpts runtime.AgentOpts
}

func (m *mockRuntime) Spawn(_ context.Context, _ sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	m.lastOpts = opts
	return &mockProcess{}, nil
}
func (m *mockRuntime) Name() string        { return "mock" }
func (m *mockRuntime) SupportsRPC() bool   { return false }
func (m *mockRuntime) SupportsHooks() bool { return false }

type mockProcess struct{}

func (m *mockProcess) Send(_ context.Context, _ runtime.AgentMessage) error { return nil }
func (m *mockProcess) Output() <-chan runtime.AgentEvent {
	ch := make(chan runtime.AgentEvent)
	close(ch)
	return ch
}
func (m *mockProcess) Wait() (runtime.AgentResult, error) {
	return runtime.AgentResult{Success: true, Summary: "done"}, nil
}
func (m *mockProcess) Kill() error { return nil }

// mockSandboxProvider tracks created sandboxes in memory.
type mockSandboxProvider struct {
	sandboxes map[string]*mockSandboxEntry
}

func newMockSandboxProvider() *mockSandboxProvider {
	return &mockSandboxProvider{sandboxes: make(map[string]*mockSandboxEntry)}
}

func (m *mockSandboxProvider) Create(_ context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
	sb := &mockSandboxEntry{id: opts.Name, labels: opts.Labels, envVars: opts.EnvVars}
	m.sandboxes[sb.id] = sb
	return sb, nil
}
func (m *mockSandboxProvider) Get(_ context.Context, id string) (sandbox.Sandbox, error) {
	sb, ok := m.sandboxes[id]
	if !ok {
		return nil, fmt.Errorf("sandbox %s not found", id)
	}
	return sb, nil
}
func (m *mockSandboxProvider) List(_ context.Context, _ map[string]string) ([]sandbox.Sandbox, error) {
	return nil, nil
}
func (m *mockSandboxProvider) Delete(_ context.Context, id string) error {
	delete(m.sandboxes, id)
	return nil
}

type mockSandboxEntry struct {
	id      string
	labels  map[string]string
	envVars map[string]string
}

func (m *mockSandboxEntry) ID() string                    { return m.id }
func (m *mockSandboxEntry) Status() sandbox.SandboxStatus { return sandbox.SandboxStatusRunning }
func (m *mockSandboxEntry) Exec(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{}, nil
}
func (m *mockSandboxEntry) ExecStreaming(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	return nil, fmt.Errorf("ExecStreaming not implemented in mock")
}
func (m *mockSandboxEntry) Upload(_ context.Context, _ []byte, _ string) error   { return nil }
func (m *mockSandboxEntry) Download(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (m *mockSandboxEntry) Stop(_ context.Context) error                         { return nil }
func (m *mockSandboxEntry) Start(_ context.Context) error                        { return nil }
func (m *mockSandboxEntry) Health() error                                        { return nil }

func setupSpawnerTest(t *testing.T) (*Spawner, *mockRuntime, *mockSandboxProvider, *db.AgentStore, *events.PersistentBus) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	agentStore := db.NewAgentStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())
	bus := events.NewPersistentBus(eventStore, slog.Default())
	rt := &mockRuntime{}
	sp := newMockSandboxProvider()
	rulesEng := rules.NewEngine(slog.Default())
	toolCurator := tools.NewCurator(slog.Default())

	spawner := NewSpawner(agentStore, rt, sp, rulesEng, toolCurator, bus, nil, "", slog.Default(), "http://localhost:8080", "Tack", "tack@local")
	return spawner, rt, sp, agentStore, bus
}

func makeSpawnObjective(id string) *domain.Objective {
	return &domain.Objective{
		ID:          id,
		Description: "test objective for spawner",
		Status:      domain.ObjectiveStatusApproved,
	}
}

func TestSpawn_InjectsGitIdentityIntoSandboxAndAgentEnv(t *testing.T) {
	spawner, rt, sp, _, _ := setupSpawnerTest(t)
	ctx := context.Background()

	obj := makeSpawnObjective("obj-git-env-1234")
	req := SpawnRequest{Objective: obj, Role: "builder", TaskSpec: "implement feature"}

	result, err := spawner.Spawn(ctx, req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	sb := sp.sandboxes[result.Sandbox.ID()]
	if sb == nil {
		t.Fatalf("expected sandbox entry for %s", result.Sandbox.ID())
	}
	if got := sb.envVars["GIT_AUTHOR_NAME"]; got != "Tack" {
		t.Fatalf("sandbox GIT_AUTHOR_NAME = %q, want %q", got, "Tack")
	}
	if got := sb.envVars["GIT_AUTHOR_EMAIL"]; got != "tack@local" {
		t.Fatalf("sandbox GIT_AUTHOR_EMAIL = %q, want %q", got, "tack@local")
	}
	if got := rt.lastOpts.EnvVars["GIT_COMMITTER_NAME"]; got != "Tack" {
		t.Fatalf("agent GIT_COMMITTER_NAME = %q, want %q", got, "Tack")
	}
	if got := rt.lastOpts.EnvVars["GIT_COMMITTER_EMAIL"]; got != "tack@local" {
		t.Fatalf("agent GIT_COMMITTER_EMAIL = %q, want %q", got, "tack@local")
	}
}

func TestSpawn_CreatesAgentSessionSandboxAndProcess(t *testing.T) {
	spawner, _, sp, agentStore, _ := setupSpawnerTest(t)
	ctx := context.Background()

	obj := makeSpawnObjective("obj-spawn-1234")
	req := SpawnRequest{Objective: obj, Role: "builder", TaskSpec: "implement feature"}

	result, err := spawner.Spawn(ctx, req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if result.Session == nil {
		t.Fatal("expected Session to be non-nil")
	}
	if result.Process == nil {
		t.Fatal("expected Process to be non-nil")
	}
	if result.Sandbox == nil {
		t.Fatal("expected Sandbox to be non-nil")
	}

	// Session should be persisted and marked running
	got, err := agentStore.Get(ctx, result.Session.ID)
	if err != nil {
		t.Fatalf("Get agent session: %v", err)
	}
	if got.Role != "builder" {
		t.Errorf("role = %q, want builder", got.Role)
	}
	if got.Status != "running" {
		t.Errorf("status = %q, want running", got.Status)
	}

	// Sandbox should be present in the provider
	if len(sp.sandboxes) != 1 {
		t.Errorf("expected 1 sandbox in provider, got %d", len(sp.sandboxes))
	}
}

func TestSpawn_AssemblesOverlayWithMatchedRulesAndCuratedTools(t *testing.T) {
	spawner, rt, _, _, _ := setupSpawnerTest(t)
	ctx := context.Background()

	obj := makeSpawnObjective("obj-overlay-1234")
	req := SpawnRequest{
		Objective: obj,
		Role:      "lead",
		TaskSpec:  "manage stream",
		Stream: &domain.Stream{
			ID:          "stream-1",
			Title:       "Auth stream",
			FileScope:   []string{"internal/auth/**"},
			Description: "Authentication work",
		},
	}

	_, err := spawner.Spawn(ctx, req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if rt.lastOpts.Overlay == "" {
		t.Error("expected non-empty overlay")
	}
	if !strings.Contains(rt.lastOpts.Overlay, obj.Description) {
		t.Errorf("overlay does not contain objective description %q", obj.Description)
	}
	if rt.lastOpts.Role != "lead" {
		t.Errorf("role in opts = %q, want lead", rt.lastOpts.Role)
	}
}

func TestSpawn_UsesBuildPlannerOverlayForPlannerRole(t *testing.T) {
	spawner, rt, _, _, _ := setupSpawnerTest(t)
	ctx := context.Background()

	obj := makeSpawnObjective("obj-planner-5678")
	req := SpawnRequest{Objective: obj, Role: "planner"}

	_, err := spawner.Spawn(ctx, req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// BuildPlannerOverlay produces "# Tack Agent: planner" and "You are a Planner agent"
	if !strings.Contains(rt.lastOpts.Overlay, "Planner") {
		t.Errorf("planner overlay should contain 'Planner'\noverlay: %s", rt.lastOpts.Overlay)
	}
}

func TestSpawn_PublishesEventAgentSpawned(t *testing.T) {
	spawner, _, _, _, bus := setupSpawnerTest(t)
	ctx := context.Background()

	sub, unsub := bus.Subscribe(10)
	defer unsub()

	obj := makeSpawnObjective("obj-event-9012")
	req := SpawnRequest{Objective: obj, Role: "builder"}

	if _, err := spawner.Spawn(ctx, req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	var gotSpawned bool
drain:
	for {
		select {
		case ev := <-sub:
			if ev.Type == domain.EventAgentSpawned {
				gotSpawned = true
			}
		default:
			break drain
		}
	}
	if !gotSpawned {
		t.Error("expected EventAgentSpawned to be published")
	}
}

func TestKill_TerminatesProcessAndMarksSessionFailed(t *testing.T) {
	spawner, _, _, agentStore, bus := setupSpawnerTest(t)
	ctx := context.Background()

	// Spawn an agent first to get a session to kill
	obj := makeSpawnObjective("obj-kill-3456")
	result, err := spawner.Spawn(ctx, SpawnRequest{Objective: obj, Role: "builder"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	sub, unsub := bus.Subscribe(10)
	defer unsub()

	if err := spawner.Kill(ctx, result.Session.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Session should be marked failed
	got, err := agentStore.Get(ctx, result.Session.ID)
	if err != nil {
		t.Fatalf("Get agent session: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}

	// EventAgentFailed should be published
	var gotFailed bool
drainFailed:
	for {
		select {
		case ev := <-sub:
			if ev.Type == domain.EventAgentFailed {
				gotFailed = true
			}
		default:
			break drainFailed
		}
	}
	if !gotFailed {
		t.Error("expected EventAgentFailed to be published after Kill")
	}
}

func TestFindSandboxForStep_FiltersByStreamAndRole(t *testing.T) {
	spawner, _, _, _, _ := setupSpawnerTest(t)
	ctx := context.Background()
	obj := makeSpawnObjective("obj-find-sandbox-1234")

	builderA, err := spawner.Spawn(ctx, SpawnRequest{
		Objective: obj,
		Role:      "builder",
		Stream:    &domain.Stream{ID: "stream-a", Title: "A"},
	})
	if err != nil {
		t.Fatalf("Spawn builderA: %v", err)
	}
	if _, err := spawner.Spawn(ctx, SpawnRequest{
		Objective: obj,
		Role:      "reviewer",
		Stream:    &domain.Stream{ID: "stream-a", Title: "A"},
	}); err != nil {
		t.Fatalf("Spawn reviewerA: %v", err)
	}
	if _, err := spawner.Spawn(ctx, SpawnRequest{
		Objective: obj,
		Role:      "builder",
		Stream:    &domain.Stream{ID: "stream-b", Title: "B"},
	}); err != nil {
		t.Fatalf("Spawn builderB: %v", err)
	}

	sb, err := spawner.FindSandboxForStep(ctx, obj.ID, "stream-a", "builder")
	if err != nil {
		t.Fatalf("FindSandboxForStep: %v", err)
	}
	if sb.ID() != builderA.Sandbox.ID() {
		t.Fatalf("sandbox = %q, want %q", sb.ID(), builderA.Sandbox.ID())
	}
}

func TestFindSandboxForStep_PrefersMostRecentMatch(t *testing.T) {
	spawner, _, _, _, _ := setupSpawnerTest(t)
	ctx := context.Background()
	obj := makeSpawnObjective("obj-find-latest-5678")
	stream := &domain.Stream{ID: "stream-a", Title: "A"}

	first, err := spawner.Spawn(ctx, SpawnRequest{Objective: obj, Role: "builder", Stream: stream})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	// AgentStore ordering uses created_at DESC with second-level precision, so
	// ensure the second session lands in a later second.
	time.Sleep(1100 * time.Millisecond)
	second, err := spawner.Spawn(ctx, SpawnRequest{Objective: obj, Role: "builder", Stream: stream})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}

	sb, err := spawner.FindSandboxForStep(ctx, obj.ID, stream.ID, "builder")
	if err != nil {
		t.Fatalf("FindSandboxForStep: %v", err)
	}
	if sb.ID() != second.Sandbox.ID() {
		t.Fatalf("sandbox = %q, want most recent %q (first was %q)", sb.ID(), second.Sandbox.ID(), first.Sandbox.ID())
	}
}
