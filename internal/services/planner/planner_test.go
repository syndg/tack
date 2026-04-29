package planner

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

type mockRunController struct {
	ensureCalled bool
	ensureID     string
	ensureErr    error

	commandCalled bool
	commandRunID  string
	command       domain.Command
	commandErr    error
}

func (m *mockRunController) Ensure(ctx context.Context, objectiveID string) (domain.Snapshot, error) {
	m.ensureCalled = true
	m.ensureID = objectiveID
	return domain.Snapshot{}, m.ensureErr
}

func (m *mockRunController) Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error) {
	m.commandCalled = true
	m.commandRunID = runID
	m.command = cmd
	return domain.Snapshot{}, m.commandErr
}

// setupService builds a complete planner.Service backed by a fresh SQLite DB.
func setupService(t *testing.T) (*Service, *db.ObjectiveStore, *db.PlanStore, *db.StreamStore, *db.RunStore, *db.ObjectiveInsightStore) {
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

	objStore := db.NewObjectiveStore(d.Conn())
	planStore := db.NewPlanStore(d.Conn())
	streamStore := db.NewStreamStore(d.Conn())
	dossierStore := db.NewDossierStore(d.Conn())
	agentStore := db.NewAgentStore(d.Conn())
	runStore := db.NewRunStore(d.Conn())
	insightStore := db.NewObjectiveInsightStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())

	bus := events.NewPersistentBus(eventStore, slog.Default())
	logger := slog.Default()

	lcm := lifecycle.New(objStore, planStore, streamStore, agentStore, bus, nil, logger)
	svc := New(planStore, streamStore, dossierStore, objStore, agentStore, lcm, bus, nil, logger, []string{"go test ./...", "go vet ./..."})
	svc.BindInsightStore(insightStore)
	return svc, objStore, planStore, streamStore, runStore, insightStore
}

// validPlanYAML is raw YAML (no code fence) for testing CreatePlan.
const validPlanYAML = `streams:
  - title: "auth refactor"
    description: "Refactor auth module"
    file_scope:
      - "src/auth/**"
    dependencies: []
  - title: "update tests"
    description: "Update test suite"
    file_scope:
      - "tests/**"
    dependencies:
      - "auth refactor"
quality_gates:
  - "go test ./..."`

func TestCreatePlan_EndToEnd(t *testing.T) {
	svc, objStore, planStore, streamStore, _, _ := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "refactor auth"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	plan, err := svc.CreatePlan(ctx, obj.ID, validPlanYAML)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if plan.ID == "" {
		t.Error("expected plan ID to be set")
	}
	if plan.Status != domain.PlanStatusPendingApproval {
		t.Errorf("plan status = %q, want pending_approval", plan.Status)
	}

	// Verify streams were persisted.
	streams, err := streamStore.ListByPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListByPlan: %v", err)
	}
	if len(streams) != 2 {
		t.Errorf("streams = %d, want 2", len(streams))
	}
	if streams[0].Card == nil || streams[0].Card.Goal != "Refactor auth module" {
		t.Fatalf("stream card = %#v, want hydrated card", streams[0].Card)
	}

	// Verify plan is retrievable with correct status.
	got, err := planStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get plan: %v", err)
	}
	if got.Status != domain.PlanStatusPendingApproval {
		t.Errorf("persisted plan status = %q, want pending_approval", got.Status)
	}
}

func TestCreatePlan_FallsBackToDefaultQualityGatesForNaturalLanguageGates(t *testing.T) {
	svc, objStore, planStore, _, _, _ := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "update health endpoint"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	_, err := svc.CreatePlan(ctx, obj.ID, `streams:
  - title: "health"
    description: "Update health endpoint"
    file_scope:
      - "src/index.ts"
    dependencies: []
quality_gates:
  - "Run the repository's discovered health test command"
  - "Run the discovered targeted validation command"`)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	plan, err := planStore.GetByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if len(plan.QualityGates) != 2 || plan.QualityGates[0] != "go test ./..." || plan.QualityGates[1] != "go vet ./..." {
		t.Fatalf("quality gates = %#v, want defaults", plan.QualityGates)
	}
}

func TestCreatePlan_FallsBackToDefaultQualityGatesForWrongPackageManager(t *testing.T) {
	svc, objStore, planStore, _, _, _ := setupService(t)
	svc.defaultQualityGates = []string{"bun install --frozen-lockfile", "bunx tsc --noEmit", "bun test"}
	ctx := context.Background()

	obj := &domain.Objective{Description: "update health endpoint"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	_, err := svc.CreatePlan(ctx, obj.ID, `streams:
  - title: "health"
    description: "Update health endpoint"
    file_scope:
      - "src/index.ts"
    dependencies: []
quality_gates:
  - "npm test"
  - "npm test -- tests/health.test.ts"`)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	plan, err := planStore.GetByObjective(ctx, obj.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if len(plan.QualityGates) != 3 || plan.QualityGates[0] != "bun install --frozen-lockfile" || plan.QualityGates[1] != "bunx tsc --noEmit" || plan.QualityGates[2] != "bun test" {
		t.Fatalf("quality gates = %#v, want bun defaults", plan.QualityGates)
	}
}

func TestCreatePlan_AttachesContractPatchesToStreamCards(t *testing.T) {
	svc, objStore, _, streamStore, _, insightStore := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "refactor auth"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if err := insightStore.Create(ctx, &domain.ObjectiveInsight{
		ObjectiveID: obj.ID,
		Source:      domain.InsightSourceHuman,
		Kind:        domain.InsightKindRetryGuidance,
		Summary:     "Keep API compatibility stable",
	}); err != nil {
		t.Fatalf("Create global insight: %v", err)
	}

	plan, err := svc.CreatePlan(ctx, obj.ID, validPlanYAML)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	streams, err := streamStore.ListByPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListByPlan: %v", err)
	}
	for _, stream := range streams {
		if stream.Card == nil || len(stream.Card.ContractPatches) != 1 {
			t.Fatalf("stream %q contract patches = %#v", stream.Title, stream.Card)
		}
		if stream.Card.ContractPatches[0].Instruction != "Honor this explicit human retry guidance: Keep API compatibility stable" {
			t.Fatalf("stream %q patch = %#v", stream.Title, stream.Card.ContractPatches)
		}
	}
}

func TestCreatePlan_AutoAppliesRepeatedCandidatesToAllStreams(t *testing.T) {
	svc, objStore, _, streamStore, _, insightStore := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "refactor auth"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	for _, streamID := range []string{"stream-a", "stream-b"} {
		if err := insightStore.Create(ctx, &domain.ObjectiveInsight{
			ObjectiveID: obj.ID,
			StreamID:    streamID,
			Source:      domain.InsightSourceReviewer,
			Kind:        domain.InsightKindReviewRejection,
			Summary:     "Keep auth middleware coverage explicit",
		}); err != nil {
			t.Fatalf("Create repeated insight: %v", err)
		}
	}

	plan, err := svc.CreatePlan(ctx, obj.ID, validPlanYAML)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	streams, err := streamStore.ListByPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListByPlan: %v", err)
	}
	for _, stream := range streams {
		found := false
		for _, patch := range stream.Card.ContractPatches {
			if patch.CandidateID != "" && patch.Instruction == "Preserve this previously rejected requirement: Keep auth middleware coverage explicit" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stream %q missing auto-applied candidate patch: %#v", stream.Title, stream.Card.ContractPatches)
		}
	}
}

func TestCreatePlan_ReturnsNeedsDossierExpansion(t *testing.T) {
	svc, objStore, _, _, _, _ := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "refactor auth"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	_, err := svc.CreatePlan(ctx, obj.ID, `PLANNER_OUTCOME: needs_dossier_expansion
PLANNER_REASON: Missing evidence for auth entrypoints
PLANNER_FOCUS_AREAS:
- auth handlers
PLANNER_FILE_HINTS:
- src/auth/handlers/**
PLANNER_QUESTIONS:
- Which handlers still bypass middleware?`)
	if err == nil {
		t.Fatal("expected dossier expansion error")
	}
	expansionErr, ok := err.(*NeedsDossierExpansionError)
	if !ok {
		t.Fatalf("error = %T, want *NeedsDossierExpansionError", err)
	}
	if expansionErr.Request.Reason != "Missing evidence for auth entrypoints" {
		t.Fatalf("reason = %q", expansionErr.Request.Reason)
	}
	if len(expansionErr.Request.FocusAreas) != 1 || expansionErr.Request.FocusAreas[0] != "auth handlers" {
		t.Fatalf("focus areas = %#v", expansionErr.Request.FocusAreas)
	}
}

func TestCreateSimplePlan_SingleStream(t *testing.T) {
	svc, objStore, _, streamStore, _, insightStore := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "fix typo in header"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if err := insightStore.Create(ctx, &domain.ObjectiveInsight{ObjectiveID: obj.ID, Source: domain.InsightSourcePlanner, Kind: domain.InsightKindPlanApproval, Summary: "Keep the simple patch tightly scoped"}); err != nil {
		t.Fatalf("Create insight: %v", err)
	}

	plan, err := svc.CreateSimplePlan(ctx, obj.ID)
	if err != nil {
		t.Fatalf("CreateSimplePlan: %v", err)
	}
	if plan.Status != domain.PlanStatusPendingApproval {
		t.Errorf("status = %q, want pending_approval", plan.Status)
	}
	if len(plan.QualityGates) != 2 || plan.QualityGates[0] != "go test ./..." || plan.QualityGates[1] != "go vet ./..." {
		t.Errorf("quality gates = %v, want default gates", plan.QualityGates)
	}

	streams, err := streamStore.ListByPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListByPlan: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(streams))
	}
	if len(streams[0].FileScope) != 1 || streams[0].FileScope[0] != "**/*" {
		t.Errorf("file_scope = %v, want [\"**/*\"]", streams[0].FileScope)
	}
	if streams[0].Card == nil || streams[0].Card.Goal != obj.Description {
		t.Fatalf("card = %#v, want simple stream card", streams[0].Card)
	}
	if len(streams[0].Card.ContractPatches) != 1 {
		t.Fatalf("contract patches = %#v", streams[0].Card.ContractPatches)
	}
	if streams[0].Title != obj.Description {
		t.Errorf("stream title = %q, want %q", streams[0].Title, obj.Description)
	}
	if len(streams[0].Dependencies) != 0 {
		t.Errorf("dependencies = %v, want empty", streams[0].Dependencies)
	}
}

func TestStartSimple_AutoApprove(t *testing.T) {
	svc, objStore, _, _, _, _ := setupService(t)
	ctx := context.Background()

	obj, plan, err := svc.StartSimple(ctx, "fix broken link", SimpleOpts{AutoApprove: true})
	if err != nil {
		t.Fatalf("StartSimple: %v", err)
	}
	if obj.ID == "" {
		t.Error("expected objective ID to be set")
	}
	if obj.Status != domain.ObjectiveStatusApproved {
		t.Errorf("objective status = %q, want approved", obj.Status)
	}
	if plan.Status != domain.PlanStatusApproved {
		t.Errorf("plan status = %q, want approved", plan.Status)
	}

	// Verify persisted objective reflects approved status.
	gotObj, err := objStore.Get(ctx, obj.ID)
	if err != nil {
		t.Fatalf("Get objective: %v", err)
	}
	if gotObj.Status != domain.ObjectiveStatusApproved {
		t.Errorf("persisted objective status = %q, want approved", gotObj.Status)
	}
}

func TestStartSimple_WithoutAutoApprove(t *testing.T) {
	svc, _, _, _, _, _ := setupService(t)
	ctx := context.Background()

	obj, plan, err := svc.StartSimple(ctx, "add feature", SimpleOpts{AutoApprove: false})
	if err != nil {
		t.Fatalf("StartSimple: %v", err)
	}
	// Objective stays in planning when not auto-approved.
	if obj.Status != domain.ObjectiveStatusPlanning {
		t.Errorf("objective status = %q, want planning", obj.Status)
	}
	// Plan should be pending_approval (set by CreateSimplePlan).
	if plan.Status != domain.PlanStatusPendingApproval {
		t.Errorf("plan status = %q, want pending_approval", plan.Status)
	}
}

func TestStartSimpleExecution_StartsRunWhenAutoApproved(t *testing.T) {
	svc, _, _, _, runStore, _ := setupService(t)
	ctx := context.Background()
	controller := &mockRunController{}
	svc.BindRunController(runStore, controller)

	obj, plan, err := svc.StartSimpleExecution(ctx, "fix broken link", SimpleOpts{AutoApprove: true})
	if err != nil {
		t.Fatalf("StartSimpleExecution: %v", err)
	}
	if !controller.ensureCalled {
		t.Fatal("expected run controller Ensure to be called")
	}
	if controller.ensureID != obj.ID {
		t.Fatalf("run controller ensure objective = %q, want %q", controller.ensureID, obj.ID)
	}
	if plan.Status != domain.PlanStatusApproved {
		t.Fatalf("plan status = %q, want approved", plan.Status)
	}
}

func TestApprovePlan_ResumesBlockedRun(t *testing.T) {
	svc, objStore, _, _, runStore, insightStore := setupService(t)
	ctx := context.Background()
	controller := &mockRunController{}
	svc.BindRunController(runStore, controller)

	obj := &domain.Objective{Description: "feature work"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan, err := svc.CreateSimplePlan(ctx, obj.ID)
	if err != nil {
		t.Fatalf("CreateSimplePlan: %v", err)
	}
	if err := runStore.Create(ctx, &domain.Run{ObjectiveID: obj.ID, Status: domain.RunStatusBlocked}); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	plan, err = svc.ApprovePlanWithReason(ctx, plan.ID, "Approved after validating stream boundaries")
	if err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	if plan.Status != domain.PlanStatusApproved {
		t.Fatalf("plan status = %q, want approved", plan.Status)
	}
	if !controller.commandCalled {
		t.Fatal("expected run controller Command to be called")
	}
	if controller.command.Kind != domain.CommandApprove {
		t.Fatalf("command kind = %q, want approve", controller.command.Kind)
	}
	insights, err := insightStore.ListByObjective(ctx, obj.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective insights: %v", err)
	}
	if len(insights) != 1 || insights[0].Kind != domain.InsightKindPlanApproval {
		t.Fatalf("insights = %#v", insights)
	}
}

func TestRejectPlan_ReturnsFailedPlan(t *testing.T) {
	svc, objStore, _, _, _, _ := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "feature work"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan, err := svc.CreateSimplePlan(ctx, obj.ID)
	if err != nil {
		t.Fatalf("CreateSimplePlan: %v", err)
	}

	plan, err = svc.RejectPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("RejectPlan: %v", err)
	}
	if plan.Status != domain.PlanStatusFailed {
		t.Fatalf("plan status = %q, want failed", plan.Status)
	}
}

func TestUpdatePlanQualityGatesWithReason_RecordsInsight(t *testing.T) {
	svc, objStore, _, _, _, insightStore := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "feature work"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan, err := svc.CreateSimplePlan(ctx, obj.ID)
	if err != nil {
		t.Fatalf("CreateSimplePlan: %v", err)
	}
	updated, err := svc.UpdatePlanQualityGatesWithReason(ctx, plan.ID, []string{"go test ./internal/services/planner"}, "Limit validation to planner package while iterating")
	if err != nil {
		t.Fatalf("UpdatePlanQualityGatesWithReason: %v", err)
	}
	if len(updated.QualityGates) != 1 || updated.QualityGates[0] != "go test ./internal/services/planner" {
		t.Fatalf("quality gates = %#v", updated.QualityGates)
	}
	insights, err := insightStore.ListByObjective(ctx, obj.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective insights: %v", err)
	}
	if len(insights) != 1 || insights[0].Kind != domain.InsightKindPlanQualityGateEdit {
		t.Fatalf("insights = %#v", insights)
	}
}
