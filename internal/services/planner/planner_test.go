package planner

import (
	"context"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// setupService builds a complete planner.Service backed by a fresh SQLite DB.
func setupService(t *testing.T) (*Service, *db.ObjectiveStore, *db.PlanStore, *db.StreamStore) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	objStore := db.NewObjectiveStore(d.Conn())
	planStore := db.NewPlanStore(d.Conn())
	streamStore := db.NewStreamStore(d.Conn())
	agentStore := db.NewAgentStore(d.Conn())
	eventStore := db.NewEventStore(d.Conn())

	bus := events.NewPersistentBus(eventStore, slog.Default())
	logger := slog.Default()

	lcm := lifecycle.New(objStore, planStore, streamStore, agentStore, bus, logger)
	svc := New(planStore, streamStore, objStore, agentStore, lcm, bus, logger, []string{"go test ./...", "go vet ./..."})
	return svc, objStore, planStore, streamStore
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
	svc, objStore, planStore, streamStore := setupService(t)
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

	// Verify plan is retrievable with correct status.
	got, err := planStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get plan: %v", err)
	}
	if got.Status != domain.PlanStatusPendingApproval {
		t.Errorf("persisted plan status = %q, want pending_approval", got.Status)
	}
}

func TestCreateSimplePlan_SingleStream(t *testing.T) {
	svc, objStore, _, streamStore := setupService(t)
	ctx := context.Background()

	obj := &domain.Objective{Description: "fix typo in header"}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
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
	if streams[0].Title != obj.Description {
		t.Errorf("stream title = %q, want %q", streams[0].Title, obj.Description)
	}
	if len(streams[0].Dependencies) != 0 {
		t.Errorf("dependencies = %v, want empty", streams[0].Dependencies)
	}
}

func TestStartSimple_AutoApprove(t *testing.T) {
	svc, objStore, _, _ := setupService(t)
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
	svc, _, _, _ := setupService(t)
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
