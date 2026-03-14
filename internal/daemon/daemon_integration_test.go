package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/syndg/deck/internal/client"
	"github.com/syndg/deck/internal/config"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
)

func TestDaemonIntegration(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19800"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	waitForHTTP(t, "http://"+cfg.Daemon.Listen+"/health")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Post("http://"+cfg.Daemon.Listen+"/objectives", "application/json", bytes.NewBufferString(`{"description":"  test objective  "}`))
	if err != nil {
		t.Fatalf("POST /objectives: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	c := client.New("http://" + cfg.Daemon.Listen)
	objectives, err := c.ListObjectives(context.Background())
	if err != nil {
		t.Fatalf("ListObjectives: %v", err)
	}
	if len(objectives) != 1 {
		t.Fatalf("expected 1 objective, got %d", len(objectives))
	}
	if objectives[0].Description != "test objective" {
		t.Fatalf("expected trimmed description, got %q", objectives[0].Description)
	}

	status, err := c.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Status != "ok" {
		t.Fatalf("unexpected status: %q", status.Status)
	}
	// The objective may have already transitioned from "planning" to "executing"
	// by the time we check (race with coordinator auto-start).
	inProgress := status.Objectives["planning"] + status.Objectives["executing"]
	if inProgress != 1 {
		t.Fatalf("expected 1 in-progress objective (planning+executing), got %d: %v", inProgress, status.Objectives)
	}
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", url)
}

func TestCreateObjectiveValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19801"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	waitForHTTP(t, "http://"+cfg.Daemon.Listen+"/health")

	resp, err := http.Post("http://"+cfg.Daemon.Listen+"/objectives", "application/json", bytes.NewBufferString(`{"description":"   "}`))
	if err != nil {
		t.Fatalf("POST /objectives: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if body["error"] == "" {
		t.Fatal("expected error message in response")
	}
}

func TestBlueprintEndpoints(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19802"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	baseURL := "http://" + cfg.Daemon.Listen
	waitForHTTP(t, baseURL+"/health")

	resp, err := http.Get(baseURL + "/blueprints")
	if err != nil {
		t.Fatalf("GET /blueprints: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /blueprints, got %d", resp.StatusCode)
	}

	var blueprints []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&blueprints); err != nil {
		t.Fatalf("Decode /blueprints: %v", err)
	}
	if len(blueprints) != 3 {
		t.Fatalf("expected 3 blueprints, got %d", len(blueprints))
	}

	resp, err = http.Get(baseURL + "/blueprints/Feature%20Implementation")
	if err != nil {
		t.Fatalf("GET /blueprints/{name}: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /blueprints/{name}, got %d", resp.StatusCode)
	}

	var blueprint map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&blueprint); err != nil {
		t.Fatalf("Decode /blueprints/{name}: %v", err)
	}
	if blueprint["name"] != "Feature Implementation" {
		t.Fatalf("unexpected blueprint name: %v", blueprint["name"])
	}

	resp, err = http.Get(baseURL + "/executions")
	if err != nil {
		t.Fatalf("GET /executions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /executions, got %d", resp.StatusCode)
	}

	var executions []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		t.Fatalf("Decode /executions: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("expected no executions, got %d", len(executions))
	}
}

func TestCreateObjectiveWithOptionsPersistsBlueprint(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19803"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	baseURL := "http://" + cfg.Daemon.Listen
	waitForHTTP(t, baseURL+"/health")

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "build feature X", client.CreateObjectiveOptions{
		Blueprint: "hotfix",
	})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}
	if obj.Blueprint != "hotfix" {
		t.Fatalf("blueprint = %q, want hotfix", obj.Blueprint)
	}

	got, err := c.GetObjective(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.Blueprint != "hotfix" {
		t.Fatalf("persisted blueprint = %q, want hotfix", got.Blueprint)
	}
}

func TestSimpleObjectiveUsesDefaultQualityGates(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19804"
	cfg.Daemon.DataDir = t.TempDir()
	cfg.QualityGates = []string{"go test ./...", "go vet ./..."}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	baseURL := "http://" + cfg.Daemon.Listen
	waitForHTTP(t, baseURL+"/health")

	c := client.New(baseURL)
	resp, err := c.CreateObjectiveSimple(context.Background(), "fix typo", "")
	if err != nil {
		t.Fatalf("CreateObjectiveSimple: %v", err)
	}
	if len(resp.Plan.QualityGates) != 2 {
		t.Fatalf("quality gates len = %d, want 2", len(resp.Plan.QualityGates))
	}
	if resp.Plan.QualityGates[0] != "go test ./..." || resp.Plan.QualityGates[1] != "go vet ./..." {
		t.Fatalf("quality gates = %v, want configured defaults", resp.Plan.QualityGates)
	}
}

func TestRetryMergePublishesQueuedEvent(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.db.Close()

	ctx := context.Background()
	obj := &domain.Objective{Description: "retry merge", Status: domain.ObjectiveStatusExecuting}
	if err := d.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := d.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	stream := &domain.Stream{
		PlanID:    plan.ID,
		Title:     "stream one",
		FileScope: []string{"README.md"},
		Status:    domain.StreamStatusMergeReady,
	}
	if err := d.streams.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}

	entry := &domain.MergeEntry{
		StreamID:    stream.ID,
		PlanID:      plan.ID,
		ObjectiveID: obj.ID,
		Branch:      "deck/test-stream/builder-1",
	}
	if err := d.mergeQueueStore.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue merge entry: %v", err)
	}
	if err := d.mergeQueueStore.UpdateStatus(ctx, entry.ID, domain.MergeStatusFailed, 2, "merge failed", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	sub, unsub := d.eventBus.Subscribe(10)
	defer unsub()

	req := httptest.NewRequest(http.MethodPost, "/merge-queue/"+entry.ID+"/retry", nil)
	rr := httptest.NewRecorder()
	d.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	got, err := d.mergeQueueStore.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get merge entry: %v", err)
	}
	if got.Status != domain.MergeStatusPending || got.Tier != 0 {
		t.Fatalf("merge entry after retry = status=%q tier=%d, want pending/0", got.Status, got.Tier)
	}

	select {
	case ev := <-sub:
		if ev.Type != domain.EventMergeQueued {
			t.Fatalf("event type = %q, want %q", ev.Type, domain.EventMergeQueued)
		}
		if ev.Objective != obj.ID || ev.Stream != stream.ID {
			t.Fatalf("event = %+v, want objective=%s stream=%s", ev, obj.ID, stream.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected EventMergeQueued after retry")
	}
}

func TestListMergeQueueReturnsAllEntriesByDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.db.Close()

	ctx := context.Background()
	obj := &domain.Objective{Description: "list merge queue", Status: domain.ObjectiveStatusExecuting}
	if err := d.objectives.Create(ctx, obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	plan := &domain.Plan{ObjectiveID: obj.ID}
	if err := d.plans.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}

	e1 := &domain.MergeEntry{StreamID: "stream-1", PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "branch-1"}
	if err := d.mergeQueueStore.Enqueue(ctx, e1); err != nil {
		t.Fatalf("Enqueue e1: %v", err)
	}
	e2 := &domain.MergeEntry{StreamID: "stream-2", PlanID: plan.ID, ObjectiveID: obj.ID, Branch: "branch-2"}
	if err := d.mergeQueueStore.Enqueue(ctx, e2); err != nil {
		t.Fatalf("Enqueue e2: %v", err)
	}
	if err := d.mergeQueueStore.UpdateStatus(ctx, e2.ID, domain.MergeStatusMerged, 1, "", "{}"); err != nil {
		t.Fatalf("UpdateStatus e2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/merge-queue", nil)
	rr := httptest.NewRecorder()
	d.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /merge-queue status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var entries []domain.MergeEntry
	if err := json.NewDecoder(rr.Body).Decode(&entries); err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].ID != e1.ID || entries[1].ID != e2.ID {
		t.Fatalf("entries order = [%s, %s], want [%s, %s]", entries[0].ID, entries[1].ID, e1.ID, e2.ID)
	}
	if entries[1].Status != domain.MergeStatusMerged {
		t.Fatalf("entries[1].Status = %q, want %q", entries[1].Status, domain.MergeStatusMerged)
	}
}

func TestProjectBlueprintOverridesUserBlueprint(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")

	if err := os.MkdirAll(filepath.Join(home, ".config", "deck", "blueprints"), 0o755); err != nil {
		t.Fatalf("MkdirAll home blueprints: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".deck", "blueprints"), 0o755); err != nil {
		t.Fatalf("MkdirAll project blueprints: %v", err)
	}

	userBlueprint := `name: Hotfix
description: User override
trigger: manual
steps:
  - id: user
    type: agent
    role: builder
`
	projectBlueprint := `name: Hotfix
description: Project override
trigger: manual
steps:
  - id: project
    type: agent
    role: builder
`

	if err := os.WriteFile(filepath.Join(home, ".config", "deck", "blueprints", "hotfix.yaml"), []byte(userBlueprint), 0o644); err != nil {
		t.Fatalf("WriteFile user blueprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".deck", "blueprints", "hotfix.yaml"), []byte(projectBlueprint), 0o644); err != nil {
		t.Fatalf("WriteFile project blueprint: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(project); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.Daemon.DataDir = filepath.Join(root, "data")

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.db.Close()

	bp, ok := d.blueprintRegistry.Get("Hotfix")
	if !ok {
		t.Fatal("Hotfix blueprint not found")
	}
	if bp.Description != "Project override" {
		t.Fatalf("expected project override, got %q", bp.Description)
	}
}

func TestSimpleHotfixExecution_CompletesWithNormalizedBlueprintAndQualityGates(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	installFakeClaude(t, "#!/bin/sh\necho \"done role=$DECK_AGENT_ROLE\"\nexit 0\n")

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19805", []string{"true"})
	defer shutdown()

	c := client.New(baseURL)
	resp, err := c.CreateObjectiveSimple(context.Background(), "fix typo", "hotfix")
	if err != nil {
		t.Fatalf("CreateObjectiveSimple: %v", err)
	}

	waitForCondition(t, 10*time.Second, func() bool {
		obj, err := c.GetObjective(context.Background(), resp.Objective.ID)
		return err == nil && obj.Status == "completed"
	})

	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	if len(executions) != 1 || executions[0]["status"] != "completed" {
		t.Fatalf("executions = %#v, want one completed execution", executions)
	}

	plan := mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+resp.Objective.ID+"/plan")
	if plan.Plan.Status != "completed" {
		t.Fatalf("plan status = %q, want completed", plan.Plan.Status)
	}
	if len(plan.Streams) != 1 || plan.Streams[0].Status != "completed" {
		t.Fatalf("streams = %#v, want one completed stream", plan.Streams)
	}

	agents := mustGetJSON[[]map[string]any](t, baseURL+"/agents")
	if len(agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(agents))
	}
	if agents[0]["role"] != "builder" || agents[0]["status"] != "completed" {
		t.Fatalf("agent = %#v, want completed builder", agents[0])
	}
}

func TestFeatureExecution_PlannerRunsThenApproveAndComplete(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	// Planner outputs valid plan YAML; other roles succeed with simple output.
	installFakeClaude(t, `#!/bin/sh
if [ "$DECK_AGENT_ROLE" = "planner" ]; then
cat <<'PLAN'
streams:
  - title: "stream one"
    description: "do the work"
    file_scope:
      - "README.md"
    dependencies: []
quality_gates: []
PLAN
exit 0
fi
echo "done role=$DECK_AGENT_ROLE"
exit 0
`)

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19806", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "feature work", client.CreateObjectiveOptions{Blueprint: "Feature Implementation"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan and execution to pause at approve step.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.Get(baseURL + "/objectives/" + obj.ID + "/plan")
		if err != nil || resp.StatusCode != 200 {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&plan) == nil && plan.Plan.ID != ""
	})

	if err := c.ApprovePlan(context.Background(), plan.Plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	waitForCondition(t, 10*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "completed"
	})

	// Parent execution + one sub-execution per stream.
	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	if len(executions) != 2 {
		t.Fatalf("executions len = %d, want 2 (parent + sub)", len(executions))
	}
	var parentExec, subExec map[string]any
	for _, e := range executions {
		if e["parent_id"] == nil || e["parent_id"] == "" {
			parentExec = e
		} else {
			subExec = e
		}
	}
	if parentExec == nil || parentExec["status"] != "completed" {
		t.Fatalf("parent execution = %#v, want completed", parentExec)
	}
	if subExec == nil || subExec["status"] != "completed" {
		t.Fatalf("sub execution = %#v, want completed", subExec)
	}

	// Sub-execution spawns scout, builder, reviewer agents. Plus the planner.
	agents := mustGetJSON[[]map[string]any](t, baseURL+"/agents")
	agentRoles := make(map[string]bool)
	for _, a := range agents {
		if role, ok := a["role"].(string); ok {
			agentRoles[role] = true
		}
	}
	if !agentRoles["planner"] {
		t.Fatalf("expected planner agent, got roles: %v", agentRoles)
	}
	if !agentRoles["builder"] {
		t.Fatalf("expected builder agent, got roles: %v", agentRoles)
	}

	plan = mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+obj.ID+"/plan")
	if plan.Plan.Status != "completed" {
		t.Fatalf("plan status = %q, want completed", plan.Plan.Status)
	}
	ss := plan.Streams[0].Status
	if len(plan.Streams) != 1 || (ss != "completed" && ss != "merge_ready" && ss != "merged") {
		t.Fatalf("streams = %#v, want one completed/merge_ready/merged stream", plan.Streams)
	}
}

func TestExecutionFailure_TransitionsObjectiveAndPlanFailed(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	installFakeClaude(t, "#!/bin/sh\necho \"done role=$DECK_AGENT_ROLE\"\nexit 0\n")

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19807", []string{"false"})
	defer shutdown()

	c := client.New(baseURL)
	resp, err := c.CreateObjectiveSimple(context.Background(), "fix typo", "hotfix")
	if err != nil {
		t.Fatalf("CreateObjectiveSimple: %v", err)
	}

	waitForCondition(t, 10*time.Second, func() bool {
		obj, err := c.GetObjective(context.Background(), resp.Objective.ID)
		return err == nil && obj.Status == "failed"
	})

	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	if len(executions) != 1 || executions[0]["status"] != "failed" {
		t.Fatalf("executions = %#v, want one failed execution", executions)
	}

	plan := mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+resp.Objective.ID+"/plan")
	if plan.Plan.Status != "failed" {
		t.Fatalf("plan status = %q, want failed", plan.Plan.Status)
	}
	if len(plan.Streams) != 1 || plan.Streams[0].Status != "failed" {
		t.Fatalf("streams = %#v, want one failed stream", plan.Streams)
	}
}

func TestKillAgent_StopsExecutionAndMarksFailure(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	installFakeClaude(t, "#!/bin/sh\nsleep 5\necho \"done role=$DECK_AGENT_ROLE\"\nexit 0\n")

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19808", nil)
	defer shutdown()

	c := client.New(baseURL)
	resp, err := c.CreateObjectiveSimple(context.Background(), "fix typo", "hotfix")
	if err != nil {
		t.Fatalf("CreateObjectiveSimple: %v", err)
	}

	var runningAgentID string
	waitForCondition(t, 10*time.Second, func() bool {
		agents := mustGetJSON[[]map[string]any](t, baseURL+"/agents")
		for _, agent := range agents {
			if agent["status"] == "running" {
				runningAgentID, _ = agent["id"].(string)
				return true
			}
		}
		return false
	})

	if err := c.KillAgent(context.Background(), runningAgentID); err != nil {
		t.Fatalf("KillAgent: %v", err)
	}

	waitForCondition(t, 10*time.Second, func() bool {
		obj, err := c.GetObjective(context.Background(), resp.Objective.ID)
		return err == nil && obj.Status == "failed"
	})

	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	if len(executions) != 1 || executions[0]["status"] != "failed" {
		t.Fatalf("executions = %#v, want one failed execution", executions)
	}

	agents := mustGetJSON[[]map[string]any](t, baseURL+"/agents")
	if len(agents) != 1 || agents[0]["status"] != "failed" {
		t.Fatalf("agents = %#v, want one failed agent", agents)
	}
}

func TestMultiStreamExecution_DependencyCascadeAndPartialCompletion(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	// Planner outputs 3-stream plan; builder fails for "fail-stream" title.
	installFakeClaude(t, `#!/bin/sh
if [ "$DECK_AGENT_ROLE" = "planner" ]; then
cat <<'PLAN'
streams:
  - title: "stream one"
    description: "succeed-stream one"
    file_scope:
      - "README.md"
    dependencies: []
  - title: "fail-stream two"
    description: "fail-stream should fail"
    file_scope:
      - "README.md"
    dependencies: []
  - title: "stream three"
    description: "succeed-stream three depends on one"
    file_scope:
      - "README.md"
    dependencies:
      - "stream one"
quality_gates: []
PLAN
exit 0
fi
if [ "$DECK_AGENT_ROLE" = "builder" ] && echo "$DECK_STREAM_TITLE" | grep -q "fail-stream"; then
  echo "build failed"
  exit 1
fi
echo "done role=$DECK_AGENT_ROLE"
exit 0
`)

	baseURL, d, shutdown := startExecutionDaemonWithInstance(t, "127.0.0.1:19809", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "multi-stream feature", client.CreateObjectiveOptions{Blueprint: "Feature Implementation"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.Get(baseURL + "/objectives/" + obj.ID + "/plan")
		if err != nil || resp.StatusCode != 200 {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&plan) == nil && plan.Plan.ID != ""
	})

	if err := c.ApprovePlan(context.Background(), plan.Plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	// Wait for objective to reach "partial" (stream two fails, one and three complete).
	waitForCondition(t, 30*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "partial"
	})

	// Verify stream statuses.
	plan = mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+obj.ID+"/plan")
	statusByTitle := make(map[string]string)
	for _, s := range plan.Streams {
		statusByTitle[s.Title] = s.Status
	}

	// Successful streams may be "completed", "merge_ready", or "merged" depending on
	// how far the merge queue got before the assertion runs.
	isSuccess := func(status string) bool {
		return status == "completed" || status == "merge_ready" || status == "merged"
	}
	if !isSuccess(statusByTitle["stream one"]) {
		t.Fatalf("stream one status = %q, want completed/merge_ready/merged", statusByTitle["stream one"])
	}
	if statusByTitle["fail-stream two"] != "failed" {
		t.Fatalf("fail-stream two status = %q, want failed", statusByTitle["fail-stream two"])
	}
	if !isSuccess(statusByTitle["stream three"]) {
		t.Fatalf("stream three status = %q, want completed/merge_ready/merged", statusByTitle["stream three"])
	}

	// Verify we have parent + 3 sub-executions.
	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	if len(executions) != 4 {
		t.Fatalf("executions len = %d, want 4 (parent + 3 sub)", len(executions))
	}

	// Find the failed sub-execution.
	var failedExecID string
	for _, e := range executions {
		if e["status"] == "failed" {
			failedExecID, _ = e["id"].(string)
		}
	}
	if failedExecID == "" {
		t.Fatal("expected one failed sub-execution")
	}

	// Check that escalation event was persisted (default on_stream_failure = escalate).
	eventStore := db.NewEventStore(d.db.Conn())
	events, err := eventStore.ListByObjective(context.Background(), obj.ID, 50)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	hasEscalation := false
	for _, ev := range events {
		if ev.Type == domain.EventEscalation {
			hasEscalation = true
			break
		}
	}
	if !hasEscalation {
		t.Fatalf("expected escalation event, got %+v", events)
	}

	// Verify objective is partial (not failed or completed).
	got, err := c.GetObjective(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.Status != "partial" {
		t.Fatalf("objective status = %q, want partial", got.Status)
	}
}

func TestRetryExecution_RetriesFailedStreamWithGuidance(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()

	// Track retry attempts via a temp file. First builder call for "retry-stream" fails,
	// subsequent calls succeed. Planner outputs valid plan YAML.
	attemptFile := filepath.Join(t.TempDir(), "attempts")
	installFakeClaude(t, `#!/bin/sh
if [ "$DECK_AGENT_ROLE" = "planner" ]; then
cat <<'PLAN'
streams:
  - title: "retry-stream"
    description: "retry-stream will fail then succeed"
    file_scope:
      - "README.md"
    dependencies: []
quality_gates: []
PLAN
exit 0
fi
if [ "$DECK_AGENT_ROLE" = "builder" ] && echo "$DECK_STREAM_TITLE" | grep -q "retry-stream"; then
  count=$(cat "`+attemptFile+`" 2>/dev/null || echo 0)
  count=$((count + 1))
  echo $count > "`+attemptFile+`"
  if [ "$count" -le 1 ]; then
    echo "build failed on first attempt"
    exit 1
  fi
fi
echo "done role=$DECK_AGENT_ROLE"
exit 0
`)

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19810", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "retry feature", client.CreateObjectiveOptions{Blueprint: "Feature Implementation"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.Get(baseURL + "/objectives/" + obj.ID + "/plan")
		if err != nil || resp.StatusCode != 200 {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&plan) == nil && plan.Plan.ID != ""
	})

	if err := c.ApprovePlan(context.Background(), plan.Plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	// Wait for objective to reach "partial" (stream fails on first attempt).
	waitForCondition(t, 30*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "partial"
	})

	// Find the failed sub-execution.
	executions := mustGetJSON[[]map[string]any](t, baseURL+"/executions")
	var failedExecID string
	for _, e := range executions {
		if e["status"] == "failed" {
			failedExecID, _ = e["id"].(string)
		}
	}
	if failedExecID == "" {
		t.Fatal("expected one failed sub-execution")
	}

	// Retry with guidance.
	if err := c.RetryExecution(context.Background(), failedExecID, "fix the build"); err != nil {
		t.Fatalf("RetryExecution: %v", err)
	}

	// Wait for stream to reach "merged" — proves merge actually completed,
	// not just enqueued. The objective should follow (partial -> completed).
	waitForCondition(t, 30*time.Second, func() bool {
		p := mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+obj.ID+"/plan")
		return len(p.Streams) == 1 && p.Streams[0].Status == "merged"
	})

	// Objective must also be completed now that the stream is fully merged.
	waitForCondition(t, 5*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "completed"
	})
}

func setupGitRepo(t *testing.T) func() {
	t.Helper()
	repo := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir repo: %v", err)
	}

	runCmd := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
		}
	}

	runCmd("git", "init", "-q")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test repo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	runCmd("git", "add", "README.md")
	runCmd("git", "commit", "-q", "-m", "init")

	return func() {
		_ = os.Chdir(cwd)
	}
}

func installFakeClaude(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func startExecutionDaemon(t *testing.T, listen string, qualityGates []string) (string, func()) {
	t.Helper()
	baseURL, _, shutdown := startExecutionDaemonWithInstance(t, listen, qualityGates)
	return baseURL, shutdown
}

func startExecutionDaemonWithInstance(t *testing.T, listen string, qualityGates []string) (string, *Daemon, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.Daemon.Listen = listen
	cfg.Daemon.DataDir = t.TempDir()
	cfg.QualityGates = append([]string(nil), qualityGates...)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()

	baseURL := "http://" + cfg.Daemon.Listen
	waitForHTTP(t, baseURL+"/health")

	return baseURL, d, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func postJSON(t *testing.T, url string, body any, wantStatus int, out any) {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		t.Fatalf("POST %s: status=%d want=%d body=%v", url, resp.StatusCode, wantStatus, payload)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("Decode response: %v", err)
		}
	}
}

func mustGetJSON[T any](t *testing.T, url string) T {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var zero T
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		t.Fatalf("GET %s: status=%d body=%v", url, resp.StatusCode, payload)
		return zero
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Decode GET %s: %v", url, err)
	}
	return out
}
