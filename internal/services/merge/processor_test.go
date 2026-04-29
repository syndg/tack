package merge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/observability"
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
	insights   *db.ObjectiveInsightStore
	streams    *db.StreamStore
	plans      *db.PlanStore
	objectives *db.ObjectiveStore
	attempts   *db.AttemptStore
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
	insightStore := db.NewObjectiveInsightStore(conn)
	streamStore := db.NewStreamStore(conn)
	planStore := db.NewPlanStore(conn)
	objStore := db.NewObjectiveStore(conn)
	attemptStore := db.NewAttemptStore(conn)
	eventStore := db.NewEventStore(conn)

	logger := slog.Default()
	bus := events.NewPersistentBus(eventStore, logger)
	recorder, err := observability.New(t.TempDir(), bus, logger)
	if err != nil {
		t.Fatalf("New recorder: %v", err)
	}

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
	registry := blueprint.NewRegistry()
	if err := registry.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	processor := NewProcessor(
		"test-project",
		queueStore,
		insightStore,
		streamStore, planStore,
		objStore,
		blueprint.NewEngine(registry, logger),
		attemptStore,
		merger, differ, gateRunner, sbProvider,
		bus, recorder, config.BenchmarkConfig{}, "main", logger,
	)

	return &processorFixture{
		processor:  processor,
		queue:      queueStore,
		insights:   insightStore,
		streams:    streamStore,
		plans:      planStore,
		objectives: objStore,
		attempts:   attemptStore,
		bus:        bus,
		sbProvider: sbProvider,
		sb:         sb,
	}
}

func TestProcessNext_RunsFinalBenchmarkValidationOnLastMerge(t *testing.T) {
	f := setupProcessor(t)
	f.processor.benchmark = config.BenchmarkConfig{ID: "lazygit.undo-basic-commit-checkout", Validation: "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v"}
	ctx := context.Background()
	var callLog []string

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "s1", FileScope: []string{"**"}, Dependencies: []string{}, Status: domain.StreamStatusMergeReady}
	if err := f.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}

	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		callLog = append(callLog, cmd)
		switch cmd {
		case "git fetch origin":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case "git merge --no-edit 'branch-1'":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v":
			return sandbox.ExecResult{ExitCode: 0, Stdout: "integration ok"}, nil
		default:
			return sandbox.ExecResult{ExitCode: 0}, nil
		}
	}

	entry := &domain.MergeEntry{StreamID: stream.ID, PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "branch-1"}
	if err := f.queue.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	processed, err := f.processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !processed {
		t.Fatal("expected merge entry to be processed")
	}
	if !contains(callLog, "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v") {
		t.Fatalf("expected final benchmark validation command, calls = %v", callLog)
	}
	got, err := f.queue.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get merge entry: %v", err)
	}
	if got.Status != domain.MergeStatusMerged {
		t.Fatalf("entry status = %q, want %q", got.Status, domain.MergeStatusMerged)
	}
	insights, err := f.insights.ListByObjective(ctx, obj.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(insights) == 0 || insights[0].Kind != domain.InsightKindBenchmarkValidationPassed {
		t.Fatalf("insights = %#v, want benchmark validation pass", insights)
	}
}

func TestProcessNext_FinalBenchmarkValidationFailureFailsMerge(t *testing.T) {
	f := setupProcessor(t)
	f.processor.benchmark = config.BenchmarkConfig{ID: "lazygit.undo-basic-commit-checkout", Validation: "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v"}
	ctx := context.Background()
	var callLog []string

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "s1", FileScope: []string{"**"}, Dependencies: []string{}, Status: domain.StreamStatusMergeReady}
	if err := f.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}

	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		callLog = append(callLog, cmd)
		switch cmd {
		case "git fetch origin":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case "git merge --no-edit 'branch-1'":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v":
			return sandbox.ExecResult{ExitCode: 1, Stderr: "FAIL integration"}, nil
		case "git reset --hard ORIG_HEAD":
			return sandbox.ExecResult{ExitCode: 0}, nil
		default:
			return sandbox.ExecResult{ExitCode: 0}, nil
		}
	}

	entry := &domain.MergeEntry{StreamID: stream.ID, PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "branch-1"}
	if err := f.queue.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	f.processor.ProcessNext(ctx)

	if !contains(callLog, "go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v") {
		t.Fatalf("expected final benchmark validation command, calls = %v", callLog)
	}
	if !contains(callLog, "git reset --hard ORIG_HEAD") {
		t.Fatalf("expected merge revert after failed validation, calls = %v", callLog)
	}
	got, err := f.queue.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get merge entry: %v", err)
	}
	if got.Status != domain.MergeStatusFailed {
		t.Fatalf("entry status = %q, want %q", got.Status, domain.MergeStatusFailed)
	}
	attempts := listMergeAttempts(t, f, stream.ID, entry.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if !strings.Contains(attempts[0].ErrorSummary, "benchmark-final-validation") {
		t.Fatalf("error summary = %q, want benchmark validation context", attempts[0].ErrorSummary)
	}
	insights, err := f.insights.ListByObjective(ctx, obj.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(insights) == 0 || insights[0].Kind != domain.InsightKindBenchmarkValidationFailed {
		t.Fatalf("insights = %#v, want benchmark validation failure", insights)
	}
}

func listMergeAttempts(t *testing.T, f *processorFixture, streamID, mergeEntryID string) []domain.Attempt {
	t.Helper()
	attempts, err := f.attempts.ListByStream(context.Background(), streamID)
	if err != nil {
		t.Fatalf("ListByStream: %v", err)
	}
	filtered := make([]domain.Attempt, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.MergeEntryID == mergeEntryID {
			filtered = append(filtered, attempt)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].AttemptNumber < filtered[j].AttemptNumber
	})
	return filtered
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
		case cmd == "git merge --no-edit '"+streamID+"'":
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

func TestPublishNewlyReadyStreams_EmitsEventBusReady(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	upstream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "upstream",
		FileScope:    []string{"a/**"},
		Dependencies: []string{},
	}
	if err := f.streams.Create(ctx, upstream); err != nil {
		t.Fatalf("Create upstream stream: %v", err)
	}
	advanceStreamTo(t, f.streams, ctx, upstream.ID, domain.StreamStatusMerged)

	downstream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "downstream",
		FileScope:    []string{"b/**"},
		Dependencies: []string{upstream.ID},
	}
	if err := f.streams.Create(ctx, downstream); err != nil {
		t.Fatalf("Create downstream stream: %v", err)
	}

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()

	f.processor.publishNewlyReadyStreams(ctx, plan.ID)

	for {
		select {
		case ev := <-sub:
			if ev.Type != domain.EventStreamReady {
				continue
			}
			if ev.Stream != downstream.ID {
				continue
			}
			return
		default:
			t.Fatal("expected EventStreamReady to be published for downstream stream")
		}
	}
}

func TestPublishNewlyReadyStreams_RevivesDependencyBlockedStream(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusPartial}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	upstream := &domain.Stream{PlanID: plan.ID, Title: "upstream", FileScope: []string{"a/**"}}
	if err := f.streams.Create(ctx, upstream); err != nil {
		t.Fatalf("Create upstream stream: %v", err)
	}
	advanceStreamTo(t, f.streams, ctx, upstream.ID, domain.StreamStatusMerged)
	downstream := &domain.Stream{PlanID: plan.ID, Title: "downstream", FileScope: []string{"b/**"}, Dependencies: []string{upstream.ID}}
	if err := f.streams.Create(ctx, downstream); err != nil {
		t.Fatalf("Create downstream stream: %v", err)
	}
	advanceStreamTo(t, f.streams, ctx, downstream.ID, domain.StreamStatusFailed)

	sub, unsub := f.bus.Subscribe(10)
	defer unsub()
	f.processor.publishNewlyReadyStreams(ctx, plan.ID)

	got, err := f.streams.Get(ctx, downstream.ID)
	if err != nil {
		t.Fatalf("Get downstream: %v", err)
	}
	if got.Status != domain.StreamStatusPending {
		t.Fatalf("downstream status = %q, want pending", got.Status)
	}
	for {
		select {
		case ev := <-sub:
			if ev.Type == domain.EventStreamReady && ev.Stream == downstream.ID {
				return
			}
		default:
			t.Fatal("expected EventStreamReady to be published for revived downstream stream")
		}
	}
}

func TestProcessNext_FailedGates(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()
	sub, unsub := f.bus.Subscribe(10)
	defer unsub()
	var callLog []string

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
		callLog = append(callLog, cmd)
		switch {
		case cmd == "git fetch origin":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case strings.Contains(cmd, "git fetch origin '+refs/heads/*:refs/remotes/origin/*'"):
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git rev-parse --verify 'origin/branch-1'":
			return sandbox.ExecResult{ExitCode: 128}, nil
		case cmd == "git rev-parse HEAD":
			return sandbox.ExecResult{ExitCode: 0, Stdout: "abc123\n"}, nil
		case cmd == "git merge --no-edit 'branch-1'":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case cmd == "git diff --stat 'abc123...HEAD'":
			return sandbox.ExecResult{
				ExitCode: 0,
				Stdout:   " f.go | 1 +\n 1 file changed, 1 insertion(+)\n",
			}, nil
		case cmd == "git diff --stat HEAD~1":
			return sandbox.ExecResult{
				ExitCode: 0,
				Stdout:   " f.go | 1 +\n 1 file changed, 1 insertion(+)\n",
			}, nil
		case cmd == "go test ./...":
			// Gate fails.
			return sandbox.ExecResult{ExitCode: 1, Stderr: "FAIL"}, nil
		case cmd == "git reset --hard ORIG_HEAD":
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

	attempts := listMergeAttempts(t, f, stream.ID, entry.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1", len(attempts))
	}
	if attempts[0].FailureKind != domain.FailurePostMergeGate {
		t.Errorf("failure kind = %q, want %q", attempts[0].FailureKind, domain.FailurePostMergeGate)
	}
	if attempts[0].Action != domain.RecoveryActionRestartStream {
		t.Errorf("action = %q, want %q", attempts[0].Action, domain.RecoveryActionRestartStream)
	}
	if attempts[0].Status != domain.AttemptStatusRecorded {
		t.Errorf("status = %q, want %q", attempts[0].Status, domain.AttemptStatusRecorded)
	}
	if !strings.Contains(attempts[0].ErrorSummary, "post-merge-gate-1") {
		t.Errorf("error summary = %q, want gate context", attempts[0].ErrorSummary)
	}

	if len(callLog) == 0 || !contains(callLog, "git reset --hard ORIG_HEAD") {
		t.Fatalf("expected merge revert via ORIG_HEAD, calls = %v", callLog)
	}

	seenAttempt := false
	for {
		select {
		case ev := <-sub:
			if ev.Type == domain.EventRecoveryAttempt {
				seenAttempt = true
			}
		default:
			if !seenAttempt {
				t.Fatal("expected recovery attempt event")
			}
			return
		}
	}
}

func TestProcessNext_MergeConflictRetriesThenBlocks(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()
	obj := &domain.Objective{Description: "test", Status: domain.ObjectiveStatusExecuting}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	stream := &domain.Stream{
		PlanID:       plan.ID,
		Title:        "s1",
		FileScope:    []string{"**"},
		Dependencies: []string{},
		Status:       domain.StreamStatusMergeReady,
	}
	if err := f.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}
	sub, unsub := f.bus.Subscribe(32)
	defer unsub()

	f.sb.execFn = func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
		switch cmd {
		case "git fetch origin '+refs/heads/*:refs/remotes/origin/*'":
			return sandbox.ExecResult{ExitCode: 0}, nil
		case "git rev-parse --verify 'origin/conflict-branch'":
			return sandbox.ExecResult{ExitCode: 128}, nil
		case "git rev-parse HEAD":
			return sandbox.ExecResult{ExitCode: 0, Stdout: "abc123\n"}, nil
		case "git merge --no-edit 'conflict-branch'":
			return sandbox.ExecResult{ExitCode: 1, Stderr: "CONFLICT (content): Merge conflict in file.go"}, nil
		case "git merge -X theirs --no-edit 'conflict-branch'":
			return sandbox.ExecResult{ExitCode: 1, Stderr: "CONFLICT (content): Merge conflict in file.go"}, nil
		case "git diff --name-only --diff-filter=U":
			return sandbox.ExecResult{ExitCode: 0, Stdout: "file.go\n"}, nil
		case "git merge --abort":
			return sandbox.ExecResult{ExitCode: 0}, nil
		default:
			return sandbox.ExecResult{ExitCode: 0}, nil
		}
	}

	entry := &domain.MergeEntry{
		StreamID:    stream.ID,
		PlanID:      plan.ID,
		ObjectiveID: obj.ID,
		Branch:      "conflict-branch",
	}
	if err := f.queue.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	processed, err := f.processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext first attempt: %v", err)
	}
	if !processed {
		t.Fatal("expected first conflict attempt to process entry")
	}

	attempts := listMergeAttempts(t, f, stream.ID, entry.ID)
	if len(attempts) != 1 {
		t.Fatalf("attempt count after first run = %d, want 1", len(attempts))
	}
	if attempts[0].FailureKind != domain.FailureMergeConflict {
		t.Errorf("failure kind = %q, want %q", attempts[0].FailureKind, domain.FailureMergeConflict)
	}
	if attempts[0].Action != domain.RecoveryActionRetryMerge {
		t.Errorf("action = %q, want %q", attempts[0].Action, domain.RecoveryActionRetryMerge)
	}
	if attempts[0].Status != domain.AttemptStatusRecorded {
		t.Errorf("status = %q, want %q", attempts[0].Status, domain.AttemptStatusRecorded)
	}

	got, err := f.queue.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.MergeStatusPending {
		t.Errorf("entry status after first conflict = %q, want %q", got.Status, domain.MergeStatusPending)
	}

	streamState, err := f.streams.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if streamState.Status != domain.StreamStatusMergeReady {
		t.Errorf("stream status after first conflict = %q, want %q", streamState.Status, domain.StreamStatusMergeReady)
	}

	for i := 1; i < attempts[0].MaxAttempts; i++ {
		processed, err := f.processor.ProcessNext(ctx)
		if err != nil {
			t.Fatalf("ProcessNext retry %d: %v", i+1, err)
		}
		if !processed {
			t.Fatalf("expected retry %d to process entry", i+1)
		}
	}

	attempts = listMergeAttempts(t, f, stream.ID, entry.ID)
	if len(attempts) != attempts[0].MaxAttempts {
		t.Fatalf("attempt count after exhaustion = %d, want %d", len(attempts), attempts[0].MaxAttempts)
	}
	last := attempts[len(attempts)-1]
	if last.Action != domain.RecoveryActionAskHumanThenResume {
		t.Errorf("last action = %q, want %q", last.Action, domain.RecoveryActionAskHumanThenResume)
	}
	if last.Status != domain.AttemptStatusBlocked {
		t.Errorf("last status = %q, want %q", last.Status, domain.AttemptStatusBlocked)
	}

	got, err = f.queue.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get after exhaustion: %v", err)
	}
	if got.Status != domain.MergeStatusConflict {
		t.Errorf("entry status after exhaustion = %q, want %q", got.Status, domain.MergeStatusConflict)
	}

	streamState, err = f.streams.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream after exhaustion: %v", err)
	}
	if streamState.Status != domain.StreamStatusFailed {
		t.Errorf("stream status after exhaustion = %q, want %q", streamState.Status, domain.StreamStatusFailed)
	}

	seenAttempt := 0
	seenBlocked := false
	for {
		select {
		case ev := <-sub:
			switch ev.Type {
			case domain.EventRecoveryAttempt:
				seenAttempt++
			case domain.EventRecoveryBlocked:
				seenBlocked = true
			}
		default:
			if seenAttempt != len(attempts) {
				t.Fatalf("recovery attempt events = %d, want %d", seenAttempt, len(attempts))
			}
			if !seenBlocked {
				t.Fatal("expected recovery blocked event on exhaustion")
			}
			return
		}
	}
}

func TestProcessNext_MergeConflictUsesBlueprintRetryProfile(t *testing.T) {
	f := setupProcessor(t)
	ctx := context.Background()

	blueprintDir := t.TempDir()
	if err := os.WriteFile(blueprintDir+"/strict-merge.yaml", []byte(`id: strict-merge
name: Strict merge
retry:
  profile: strict
steps:
  - id: build
    type: agent
    role: builder
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg := blueprint.NewRegistry()
	if err := reg.LoadFromDir(blueprintDir); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	f.processor.engine = blueprint.NewEngine(reg, slog.Default())

	obj := &domain.Objective{Description: "strict merge objective", Status: domain.ObjectiveStatusExecuting, Blueprint: "strict-merge"}
	if err := f.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID, QualityGates: []string{}}
	if err := f.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	stream := &domain.Stream{PlanID: plan.ID, Title: "strict stream", FileScope: []string{"**/*.go"}, Status: domain.StreamStatusMergeReady}
	if err := f.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}
	entry := &domain.MergeEntry{ProjectID: "test-project", StreamID: stream.ID, PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "conflict-branch"}
	if err := f.queue.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	decision, attempt, err := f.processor.recordRecoveryAttempt(ctx, entry, domain.FailureMergeConflict, "merge conflict")
	if err != nil {
		t.Fatalf("recordRecoveryAttempt first: %v", err)
	}
	if decision.Action != domain.RecoveryActionRetryMerge {
		t.Fatalf("first action = %q, want %q", decision.Action, domain.RecoveryActionRetryMerge)
	}
	if attempt.MaxAttempts != 2 {
		t.Fatalf("max_attempts = %d, want 2", attempt.MaxAttempts)
	}

	decision, attempt, err = f.processor.recordRecoveryAttempt(ctx, entry, domain.FailureMergeConflict, "merge conflict")
	if err != nil {
		t.Fatalf("recordRecoveryAttempt second: %v", err)
	}
	if decision.Action != domain.RecoveryActionAskHumanThenResume {
		t.Fatalf("second action = %q, want %q", decision.Action, domain.RecoveryActionAskHumanThenResume)
	}
	if attempt.Status != domain.AttemptStatusBlocked {
		t.Fatalf("second status = %q, want %q", attempt.Status, domain.AttemptStatusBlocked)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
