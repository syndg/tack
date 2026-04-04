package merge

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/sandbox"
	events "github.com/syndg/tack/internal/services/events"
)

// advanceStreamTo walks a stream through valid transitions from pending to the target status.
func advanceStreamTo(t *testing.T, store *db.StreamStore, ctx context.Context, id string, target domain.StreamStatus) {
	t.Helper()
	paths := map[domain.StreamStatus][]domain.StreamStatus{
		domain.StreamStatusExecuting:  {domain.StreamStatusExecuting},
		domain.StreamStatusCompleted:  {domain.StreamStatusExecuting, domain.StreamStatusCompleted},
		domain.StreamStatusFailed:     {domain.StreamStatusExecuting, domain.StreamStatusFailed},
		domain.StreamStatusMergeReady: {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady},
		domain.StreamStatusMerging:    {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady, domain.StreamStatusMerging},
		domain.StreamStatusMerged:     {domain.StreamStatusExecuting, domain.StreamStatusCompleted, domain.StreamStatusMergeReady, domain.StreamStatusMerging, domain.StreamStatusMerged},
	}
	for _, step := range paths[target] {
		if err := store.UpdateStatus(ctx, id, step); err != nil {
			t.Fatalf("advancing stream to %s (step %s): %v", target, step, err)
		}
	}
}

// mockSandboxProvider implements sandbox.SandboxProvider for testing.
type mockSandboxProvider struct {
	sandboxes []sandbox.Sandbox
	createFn  func(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error)
}

func (m *mockSandboxProvider) Create(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
	if m.createFn != nil {
		return m.createFn(ctx, opts)
	}
	if len(m.sandboxes) > 0 {
		return m.sandboxes[0], nil
	}
	return nil, fmt.Errorf("no sandbox available")
}

func (m *mockSandboxProvider) Get(_ context.Context, id string) (sandbox.Sandbox, error) {
	for _, sb := range m.sandboxes {
		if sb.ID() == id {
			return sb, nil
		}
	}
	return nil, fmt.Errorf("sandbox %s not found", id)
}

func (m *mockSandboxProvider) List(_ context.Context, labels map[string]string) ([]sandbox.Sandbox, error) {
	return m.sandboxes, nil
}

func (m *mockSandboxProvider) Delete(_ context.Context, _ string) error {
	return nil
}

// processorFixture sets up a full processor with real DB stores and mock sandbox.
type processorFixture struct {
	processor  *Processor
	queue      *db.MergeQueueStore
	streams    *db.StreamStore
	plans      *db.PlanStore
	objectives *db.ObjectiveStore
	bus        *events.PersistentBus
	sbProvider *mockSandboxProvider
	sb         *mockSandbox
}

func setupProcessor(t *testing.T) *processorFixture {
	t.Helper()

	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.NewProjectStore(d.Conn()).Upsert(context.Background(), &domain.Project{ID: "test-project", Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir() + "/.tack/config.yaml"}); err != nil {
		t.Fatalf("register test project: %v", err)
	}

	conn := d.Conn()
	queueStore := db.NewMergeQueueStore(conn)
	streamStore := db.NewStreamStore(conn)
	planStore := db.NewPlanStore(conn)
	objStore := db.NewObjectiveStore(conn)
	eventStore := db.NewEventStore(conn)

	logger := slog.Default()
	bus := events.NewPersistentBus(eventStore, logger)

	sb := &mockSandbox{
		id: "merger-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0, Stdout: ""}, nil
		},
	}

	sbProvider := &mockSandboxProvider{
		sandboxes: []sandbox.Sandbox{sb},
	}

	merger := NewGitMerger(logger)
	differ := NewDiffExtractor(logger)
	gateRunner := gates.NewRunner(logger)

	processor := NewProcessor(
		"test-project",
		queueStore, streamStore, planStore,
		merger, differ, gateRunner, sbProvider,
		bus, "main", logger,
	)

	return &processorFixture{
		processor:  processor,
		queue:      queueStore,
		streams:    streamStore,
		plans:      planStore,
		objectives: objStore,
		bus:        bus,
		sbProvider: sbProvider,
		sb:         sb,
	}
}

// createTestData inserts an objective, plan, and stream and returns their IDs.
func createTestData(t *testing.T, f *processorFixture) (objID, planID, streamID string) {
	t.Helper()
	ctx := context.Background()

	obj := &domain.Objective{Description: "test objective", Status: domain.ObjectiveStatusExecuting}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	stream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "test stream",
		FileScope:    []string{"**/*.go"},
		Dependencies: []string{},
		Status:       domain.StreamStatusMergeReady,
	}
	if err := f.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}

	return obj.ID, plan.ID, stream.ID
}

func TestEnqueueStream(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()
	_, _, streamID := createTestData(t, f)

	// Mock sandbox to return a branch name for the stream.
	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		if cmd == "git rev-parse --abbrev-ref HEAD" {
			return sandbox.ExecResult{ExitCode: 0, Stdout: "tack/stream-1/builder-abc\n"}, nil
		}
		return sandbox.ExecResult{ExitCode: 0}, nil
	}

	// Subscribe to events before enqueuing.
	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	if err := f.processor.EnqueueStream(ctx, streamID); err != nil {
		t.Fatalf("EnqueueStream: %v", err)
	}

	// Verify entry was created.
	count, err := f.queue.CountPending(ctx)
	if err != nil {
		t.Fatalf("CountPending: %v", err)
	}
	if count != 1 {
		t.Errorf("pending count = %d, want 1", count)
	}

	// Verify the entry has correct fields.
	entry, err := f.queue.GetByStream(ctx, streamID)
	if err != nil {
		t.Fatalf("GetByStream: %v", err)
	}
	if entry.Branch != "tack/stream-1/builder-abc" {
		t.Errorf("Branch = %q, want %q", entry.Branch, "tack/stream-1/builder-abc")
	}
	if entry.Status != domain.MergeStatusPending {
		t.Errorf("Status = %q, want %q", entry.Status, domain.MergeStatusPending)
	}

	// Verify event was published.
	select {
	case ev := <-sub:
		if ev.Type != domain.EventMergeQueued {
			t.Errorf("event type = %q, want %q", ev.Type, domain.EventMergeQueued)
		}
	default:
		t.Error("expected EventMergeQueued to be published")
	}
}

func TestProcessNext_EmptyQueue(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	processed, err := f.processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if processed {
		t.Error("expected false for empty queue")
	}
}

func TestProcessNext_SkipsUnsatisfiedDependencies(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	// Create stream 1 (no deps) and stream 2 (depends on stream 1).
	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
		Status: domain.StreamStatusMergeReady,
	}
	f.streams.Create(ctx, s1)

	s2 := &domain.Stream{
		PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{s1.ID},
		Status: domain.StreamStatusMergeReady,
	}
	f.streams.Create(ctx, s2)

	// Only enqueue s2 (which depends on s1) — s1 has no merge entry.
	f.queue.Enqueue(ctx, &domain.MergeEntry{
		StreamID:    s2.ID,
		PlanID:      plan.ID,
		ObjectiveID: obj.ID,
		Branch:      "b2",
	})

	processed, err := f.processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if processed {
		t.Error("expected false — s2's dependency (s1) is not merged")
	}

	// Verify s2 is still pending.
	count, _ := f.queue.CountPending(ctx)
	if count != 1 {
		t.Errorf("pending count = %d, want 1 (entry should remain)", count)
	}
}

func TestProcessNext_SuccessfulMerge(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()
	objID, planID, streamID := createTestData(t, f)

	// Configure sandbox to simulate successful merge.
	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		switch {
		case cmd == "git fetch origin":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git merge --no-edit "+streamID:
			// Branch name used is streamID here for simplicity.
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git diff --stat HEAD~1":
			return sandbox.ExecResult{
				ExitCode: 0,
				Stdout:   " file.go | 5 +++++\n 1 file changed, 5 insertions(+)\n",
			}, nil
		default:
			return sandbox.ExecResult{ExitCode: 0, Stdout: ""}, nil
		}
	}

	// Enqueue the merge entry directly (bypass EnqueueStream which needs sandbox branch lookup).
	entry := &domain.MergeEntry{
		StreamID:    streamID,
		PlanID:      planID,
		ObjectiveID: objID,
		Branch:      streamID, // Use streamID as branch name.
	}
	f.queue.Enqueue(ctx, entry)

	processed, err := f.processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !processed {
		t.Error("expected true — entry should be processed")
	}

	// Verify entry status is merged.
	got, err := f.queue.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.MergeStatusMerged {
		t.Errorf("entry status = %q, want %q", got.Status, domain.MergeStatusMerged)
	}
	if got.Tier != 1 {
		t.Errorf("tier = %d, want 1", got.Tier)
	}

	// Verify stream status is merged.
	stream, err := f.streams.Get(ctx, streamID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if stream.Status != domain.StreamStatusMerged {
		t.Errorf("stream status = %q, want %q", stream.Status, domain.StreamStatusMerged)
	}
}

func TestProcessNext_FailedGates(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	// Create objective, plan with quality gates, and stream.
	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{"go test ./..."}}
	f.plans.Create(ctx, plan)
	stream := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"**"},
		Dependencies: []string{}, Status: domain.StreamStatusMergeReady,
	}
	f.streams.Create(ctx, stream)

	// Configure sandbox: merge succeeds but quality gate fails.
	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		switch {
		case cmd == "git fetch origin":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git merge --no-edit branch-1":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git diff --stat HEAD~1":
			return sandbox.ExecResult{
				ExitCode: 0,
				Stdout:   " f.go | 1 +\n 1 file changed, 1 insertion(+)\n",
			}, nil
		case cmd == "go test ./...":
			// Gate fails.
			return sandbox.ExecResult{ExitCode: 1, Stderr: "FAIL"}, nil
		case cmd == "git reset --hard HEAD~1":
			// Revert after failed gates.
			return sandbox.ExecResult{ExitCode: 0}, nil
		default:
			return sandbox.ExecResult{ExitCode: 0}, nil
		}
	}

	entry := &domain.MergeEntry{
		StreamID:    stream.ID,
		PlanID:      plan.ID,
		ObjectiveID: obj.ID,
		Branch:      "branch-1",
	}
	f.queue.Enqueue(ctx, entry)

	f.processor.ProcessNext(ctx)

	// Verify entry is failed.
	got, _ := f.queue.Get(ctx, entry.ID)
	if got.Status != domain.MergeStatusFailed {
		t.Errorf("entry status = %q, want %q", got.Status, domain.MergeStatusFailed)
	}
}

func TestCheckObjectiveComplete(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	// Create two streams, both merged.
	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s1)
	advanceStreamTo(t, f.streams, ctx, s1.ID, domain.StreamStatusMerged)

	s2 := &domain.Stream{
		PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s2)
	advanceStreamTo(t, f.streams, ctx, s2.ID, domain.StreamStatusMerged)

	if err := f.processor.checkObjectiveComplete(ctx, obj.ID); err != nil {
		t.Fatalf("checkObjectiveComplete: %v", err)
	}

	// Verify objective status is unchanged — the merge processor no longer
	// transitions objectives; the blueprint engine (mark_complete) does that.
	got, err := f.objectives.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if got.Status != domain.ObjectiveStatusExecuting {
		t.Errorf("objective status = %q, want %q (unchanged)", got.Status, domain.ObjectiveStatusExecuting)
	}
}

func TestCheckObjectiveComplete_NotAllMerged(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s1)
	advanceStreamTo(t, f.streams, ctx, s1.ID, domain.StreamStatusMerged)

	// s2 is still pending — not merged.
	s2 := &domain.Stream{
		PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s2)

	if err := f.processor.checkObjectiveComplete(ctx, obj.ID); err != nil {
		t.Fatalf("checkObjectiveComplete: %v", err)
	}

	// Objective should still be executing.
	got, _ := f.objectives.Get(ctx, obj.ID)
	if got.Status != domain.ObjectiveStatusExecuting {
		t.Errorf("objective status = %q, want %q (should not transition)", got.Status, domain.ObjectiveStatusExecuting)
	}
}

func TestCheckDependencies_Satisfied(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s1)

	s2 := &domain.Stream{
		PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{s1.ID},
	}
	f.streams.Create(ctx, s2)

	// Create a merged entry for s1.
	depEntry := &domain.MergeEntry{StreamID: s1.ID, PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "b1"}
	f.queue.Enqueue(ctx, depEntry)
	f.queue.UpdateStatus(ctx, depEntry.ID, domain.MergeStatusMerged, 1, "", "")

	// Check dependencies for s2.
	entry := &domain.MergeEntry{StreamID: s2.ID}
	satisfied, err := f.processor.checkDependencies(ctx, entry)
	if err != nil {
		t.Fatalf("checkDependencies: %v", err)
	}
	if !satisfied {
		t.Error("expected dependencies to be satisfied")
	}
}

func TestCheckDependencies_Unsatisfied(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s1)

	s2 := &domain.Stream{
		PlanID: plan.ID, Title: "s2", FileScope: []string{"b/**"}, Dependencies: []string{s1.ID},
	}
	f.streams.Create(ctx, s2)

	// s1 has a merge entry but is still pending (not merged).
	depEntry := &domain.MergeEntry{StreamID: s1.ID, PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "b1"}
	f.queue.Enqueue(ctx, depEntry)

	entry := &domain.MergeEntry{StreamID: s2.ID}
	satisfied, err := f.processor.checkDependencies(ctx, entry)
	if err != nil {
		t.Fatalf("checkDependencies: %v", err)
	}
	if satisfied {
		t.Error("expected dependencies to be unsatisfied (s1 is pending, not merged)")
	}
}

func TestCheckDependencies_NoDeps(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	f.objectives.Create(ctx, obj)
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	f.plans.Create(ctx, plan)

	s1 := &domain.Stream{
		PlanID: plan.ID, Title: "s1", FileScope: []string{"a/**"}, Dependencies: []string{},
	}
	f.streams.Create(ctx, s1)

	entry := &domain.MergeEntry{StreamID: s1.ID}
	satisfied, err := f.processor.checkDependencies(ctx, entry)
	if err != nil {
		t.Fatalf("checkDependencies: %v", err)
	}
	if !satisfied {
		t.Error("expected satisfied — no dependencies")
	}
}
