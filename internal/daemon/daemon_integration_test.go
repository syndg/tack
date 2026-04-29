package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/daemonauth"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

func TestDaemonIntegration(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19800"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	registerDaemonTestProject(t, d, t.TempDir())

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
	req := authedRequest(t, http.MethodPost, "http://"+cfg.Daemon.Listen+"/objectives", bytes.NewBufferString(`{"description":"  test objective  "}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
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
	registerDaemonTestProject(t, d, t.TempDir())

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

	req := authedRequest(t, http.MethodPost, "http://"+cfg.Daemon.Listen+"/objectives", bytes.NewBufferString(`{"description":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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

func TestObjectiveInsightReportRouteIsProjectScoped(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.DataDir = t.TempDir()
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Shutdown(context.Background())
	registerDaemonTestProject(t, d, t.TempDir())

	obj := &domain.Objective{ProjectID: "test-project", Description: "Inspect captured learnings"}
	if err := d.objectives.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	if err := d.insights.Create(context.Background(), &domain.ObjectiveInsight{ProjectID: "test-project", ObjectiveID: obj.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep tests focused", CreatedAt: time.Unix(10, 0)}); err != nil {
		t.Fatalf("Create insight: %v", err)
	}

	ts := httptest.NewServer(d.authMiddleware(d.mux))
	defer ts.Close()
	req := authedRequest(t, http.MethodGet, ts.URL+"/objectives/"+obj.ID+"/insights", nil)
	req.Header.Set(projectHeader, "test-project")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET insight report: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var report struct {
		ObjectiveID string `json:"objective_id"`
		Summary     struct {
			TotalInsights int `json:"total_insights"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("Decode report: %v", err)
	}
	if report.ObjectiveID != obj.ID || report.Summary.TotalInsights != 1 {
		t.Fatalf("report = %+v", report)
	}

	wrongProjectReq := authedRequest(t, http.MethodGet, ts.URL+"/objectives/"+obj.ID+"/insights", nil)
	wrongProjectReq.Header.Set(projectHeader, "other-project")
	wrongProjectResp, err := http.DefaultClient.Do(wrongProjectReq)
	if err != nil {
		t.Fatalf("GET wrong project insight report: %v", err)
	}
	defer wrongProjectResp.Body.Close()
	if wrongProjectResp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong project status = %d", wrongProjectResp.StatusCode)
	}
}

func TestInsightPromotionRoutesAreProjectScopedAndIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.DataDir = t.TempDir()
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Shutdown(context.Background())
	registerDaemonTestProject(t, d, t.TempDir())

	obj := &domain.Objective{ProjectID: "test-project", Description: "Promote captured learning"}
	if err := d.objectives.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := d.insights.Create(context.Background(), &domain.ObjectiveInsight{ProjectID: "test-project", ObjectiveID: obj.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "Keep auth middleware coverage explicit", CreatedAt: time.Unix(int64(10+i), 0)}); err != nil {
			t.Fatalf("Create insight %d: %v", i, err)
		}
	}
	candidates, err := d.candidates.ListByObjective(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("ListByObjective candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d", len(candidates))
	}

	ts := httptest.NewServer(d.authMiddleware(d.mux))
	defer ts.Close()
	promoteReq := authedRequest(t, http.MethodPost, ts.URL+"/insights/"+candidates[0].ID+"/promote", bytes.NewBufferString(`{"target":"project-memory"}`))
	promoteReq.Header.Set(projectHeader, "test-project")
	promoteReq.Header.Set("Content-Type", "application/json")
	promoteResp, err := http.DefaultClient.Do(promoteReq)
	if err != nil {
		t.Fatalf("POST promote: %v", err)
	}
	defer promoteResp.Body.Close()
	if promoteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(promoteResp.Body)
		t.Fatalf("promote status = %d body=%s", promoteResp.StatusCode, body)
	}
	var record domain.PromotionRecord
	if err := json.NewDecoder(promoteResp.Body).Decode(&record); err != nil {
		t.Fatalf("Decode promotion: %v", err)
	}
	if record.Status != domain.PromotionStatusApproved || record.Target != domain.PromotionTargetProjectMemory || record.SourceCandidateID != candidates[0].ID {
		t.Fatalf("promotion record = %+v", record)
	}

	idempotentReq := authedRequest(t, http.MethodPost, ts.URL+"/insights/"+candidates[0].ID+"/promote", bytes.NewBufferString(`{"target":"project-memory"}`))
	idempotentReq.Header.Set(projectHeader, "test-project")
	idempotentReq.Header.Set("Content-Type", "application/json")
	idempotentResp, err := http.DefaultClient.Do(idempotentReq)
	if err != nil {
		t.Fatalf("POST idempotent promote: %v", err)
	}
	defer idempotentResp.Body.Close()
	if idempotentResp.StatusCode != http.StatusOK {
		t.Fatalf("idempotent status = %d", idempotentResp.StatusCode)
	}

	wrongProjectReq := authedRequest(t, http.MethodPost, ts.URL+"/insights/"+candidates[0].ID+"/reject", nil)
	wrongProjectReq.Header.Set(projectHeader, "other-project")
	wrongProjectResp, err := http.DefaultClient.Do(wrongProjectReq)
	if err != nil {
		t.Fatalf("POST wrong project reject: %v", err)
	}
	defer wrongProjectResp.Body.Close()
	if wrongProjectResp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong project status = %d", wrongProjectResp.StatusCode)
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
	registerDaemonTestProject(t, d, t.TempDir())

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

	resp, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/blueprints", nil))
	if err != nil {
		t.Fatalf("GET /blueprints: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /blueprints, got %d", resp.StatusCode)
	}

	var workflows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&workflows); err != nil {
		t.Fatalf("Decode /blueprints: %v", err)
	}
	if len(workflows) < 2 {
		t.Fatalf("expected at least 2 workflows, got %d", len(workflows))
	}
	seen := make(map[string]bool)
	for _, workflow := range workflows {
		if id, ok := workflow["id"].(string); ok {
			seen[id] = true
		}
	}
	if !seen["standard"] || !seen["build-review"] {
		t.Fatalf("expected standard and build-review blueprints, got ids: %v", seen)
	}

	resp, err = http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/blueprints/standard", nil))
	if err != nil {
		t.Fatalf("GET /blueprints/{name}: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /blueprints/{name}, got %d", resp.StatusCode)
	}

	var blueprint map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&blueprint); err != nil {
		t.Fatalf("Decode /blueprints/{id}: %v", err)
	}
	if blueprint["id"] != "standard" {
		t.Fatalf("unexpected blueprint id: %v", blueprint["id"])
	}

	resp, err = http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/executions", nil))
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
	registerDaemonTestProject(t, d, t.TempDir())

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
		Blueprint: "build-review",
	})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}
	if obj.Blueprint != "build-review" {
		t.Fatalf("blueprint = %q, want build-review", obj.Blueprint)
	}
	if obj.Status != domain.ObjectiveStatusExecuting {
		t.Fatalf("response objective status = %q, want executing", obj.Status)
	}

	got, err := c.GetObjective(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.Blueprint != "build-review" {
		t.Fatalf("persisted blueprint = %q, want build-review", got.Blueprint)
	}
}

func TestObjectiveDossierGeneratedAndEditable(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19810"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	projectRoot := t.TempDir()
	registerDaemonTestProject(t, d, projectRoot)

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
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "Add command log keybindings", client.CreateObjectiveOptions{Blueprint: "build-review"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	var dossier *domain.Dossier
	waitForCondition(t, 10*time.Second, func() bool {
		dossier, err = c.GetObjectiveDossier(context.Background(), obj.ID)
		return err == nil && dossier != nil && strings.TrimSpace(dossier.Summary) != ""
	})
	if dossier.BlueprintID != "build-review" {
		t.Fatalf("dossier blueprint = %q, want build-review", dossier.BlueprintID)
	}
	if len(dossier.Citations) == 0 {
		t.Fatal("expected dossier citations")
	}

	dossier.Summary = "Edited dossier summary"
	dossier.Risks = append(dossier.Risks, "manual edit")
	updated, err := c.UpdateObjectiveDossier(context.Background(), obj.ID, *dossier)
	if err != nil {
		t.Fatalf("UpdateObjectiveDossier: %v", err)
	}
	if updated.Summary != "Edited dossier summary" {
		t.Fatalf("updated summary = %q, want edited summary", updated.Summary)
	}
	if len(updated.Risks) == 0 || updated.Risks[len(updated.Risks)-1] != "manual edit" {
		t.Fatalf("updated risks = %#v, want manual edit appended", updated.Risks)
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
	registerDaemonTestProject(t, d, t.TempDir())

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
		Branch:      "tack/test-stream/builder-1",
	}
	if err := d.mergeQueueStore.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue merge entry: %v", err)
	}
	if err := d.mergeQueueStore.UpdateStatus(ctx, entry.ID, domain.MergeStatusFailed, 2, "merge failed", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := d.streams.UpdateStatus(ctx, stream.ID, domain.StreamStatusFailed); err != nil {
		t.Fatalf("Update stream failed: %v", err)
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
	streamAfterRetry, err := d.streams.Get(ctx, stream.ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if streamAfterRetry.Status != domain.StreamStatusMergeReady {
		t.Fatalf("stream after retry = %q, want merge_ready", streamAfterRetry.Status)
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
	registerDaemonTestProject(t, d, t.TempDir())

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

func TestProjectWorkflowOverridesUserWorkflow(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")

	if err := os.MkdirAll(filepath.Join(home, ".config", "tack", "blueprints"), 0o755); err != nil {
		t.Fatalf("MkdirAll home blueprints: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".tack", "blueprints"), 0o755); err != nil {
		t.Fatalf("MkdirAll project blueprints: %v", err)
	}

	userBlueprint := `id: build-review
name: User workflow
description: User override
steps:
  - id: user
    type: agent
    role: builder
`
	projectBlueprint := `id: build-review
name: Project workflow
description: Project override
steps:
  - id: project
    type: agent
    role: builder
`

	if err := os.WriteFile(filepath.Join(home, ".config", "tack", "blueprints", "build-review.yaml"), []byte(userBlueprint), 0o644); err != nil {
		t.Fatalf("WriteFile user blueprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".tack", "blueprints", "build-review.yaml"), []byte(projectBlueprint), 0o644); err != nil {
		t.Fatalf("WriteFile project blueprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".tack", "config.yaml"), []byte(testClaudeCodeConfigYAML()), 0o644); err != nil {
		t.Fatalf("WriteFile project config: %v", err)
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
	registeredProject := &domain.Project{ID: "test-project", Name: "test", RootPath: project, ConfigPath: filepath.Join(project, ".tack", "config.yaml")}
	if err := d.projectStore.Upsert(context.Background(), registeredProject); err != nil {
		t.Fatalf("register project: %v", err)
	}
	projectCtx, err := d.projectCtxs.Get(context.Background(), registeredProject.ID)
	if err != nil {
		t.Fatalf("load project context: %v", err)
	}

	bp, ok := projectCtx.BlueprintRegistry.Get("build-review")
	if !ok {
		t.Fatal("build-review blueprint not found")
	}
	if bp.Description != "Project override" {
		t.Fatalf("expected project override, got %q", bp.Description)
	}
}

func TestFeatureExecution_PlannerRunsThenApproveAndComplete(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	// Planner outputs valid plan YAML; other roles succeed with simple output.
	installFakeClaude(t, `#!/bin/sh
if [ "$TACK_AGENT_ROLE" = "planner" ]; then
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
echo "done role=$TACK_AGENT_ROLE"
exit 0
`)

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19806", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "feature work", client.CreateObjectiveOptions{Blueprint: "standard"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan and execution to pause at approve step.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/objectives/"+obj.ID+"/plan", nil))
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
	if len(plan.Streams) != 1 || (ss != domain.StreamStatusCompleted && ss != domain.StreamStatusMergeReady && ss != domain.StreamStatusMerged) {
		t.Fatalf("streams = %#v, want one completed/merge_ready/merged stream", plan.Streams)
	}
}

func TestMultiStreamExecution_DependencyCascadeAndPartialCompletion(t *testing.T) {
	restoreRepo := setupGitRepo(t)
	defer restoreRepo()
	// Planner outputs 3-stream plan; builder fails for "fail-stream" title.
	installFakeClaude(t, `#!/bin/sh
if [ "$TACK_AGENT_ROLE" = "planner" ]; then
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
if [ "$TACK_AGENT_ROLE" = "builder" ] && echo "$TACK_STREAM_TITLE" | grep -q "fail-stream"; then
  echo "build failed"
  exit 1
fi
echo "done role=$TACK_AGENT_ROLE"
exit 0
`)

	baseURL, d, shutdown := startExecutionDaemonWithInstance(t, "127.0.0.1:19809", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "multi-stream feature", client.CreateObjectiveOptions{Blueprint: "standard"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/objectives/"+obj.ID+"/plan", nil))
		if err != nil || resp.StatusCode != 200 {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&plan) == nil && plan.Plan.ID != ""
	})

	if err := c.ApprovePlan(context.Background(), plan.Plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	// ApprovePlan should also resume the blocked run.
	waitForCondition(t, 30*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "partial"
	})

	// Verify stream statuses.
	plan = mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+obj.ID+"/plan")
	statusByTitle := make(map[string]domain.StreamStatus)
	for _, s := range plan.Streams {
		statusByTitle[s.Title] = s.Status
	}

	// Successful streams may be "completed", "merge_ready", or "merged" depending on
	// how far the merge queue got before the assertion runs.
	isSuccess := func(status domain.StreamStatus) bool {
		return status == domain.StreamStatusCompleted || status == domain.StreamStatusMergeReady || status == domain.StreamStatusMerged
	}
	if !isSuccess(statusByTitle["stream one"]) {
		t.Fatalf("stream one status = %q, want completed/merge_ready/merged", statusByTitle["stream one"])
	}
	if statusByTitle["fail-stream two"] != domain.StreamStatusFailed {
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

	// Check that escalation event was persisted (default on_work_item_failure = escalate).
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
if [ "$TACK_AGENT_ROLE" = "planner" ]; then
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
if [ "$TACK_AGENT_ROLE" = "builder" ] && echo "$TACK_STREAM_TITLE" | grep -q "retry-stream"; then
  count=$(cat "`+attemptFile+`" 2>/dev/null || echo 0)
  count=$((count + 1))
  echo $count > "`+attemptFile+`"
  if [ "$count" -le 1 ]; then
    echo "build failed on first attempt"
    exit 1
  fi
fi
echo "done role=$TACK_AGENT_ROLE"
exit 0
`)

	baseURL, shutdown := startExecutionDaemon(t, "127.0.0.1:19810", nil)
	defer shutdown()

	c := client.New(baseURL)
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "retry feature", client.CreateObjectiveOptions{Blueprint: "standard"})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}

	// Wait for planner agent to create the plan.
	var plan planWithStreams
	waitForCondition(t, 10*time.Second, func() bool {
		resp, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, baseURL+"/objectives/"+obj.ID+"/plan", nil))
		if err != nil || resp.StatusCode != 200 {
			return false
		}
		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(&plan) == nil && plan.Plan.ID != ""
	})

	if err := c.ApprovePlan(context.Background(), plan.Plan.ID); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	// ApprovePlan should also resume the blocked run.
	waitForCondition(t, 30*time.Second, func() bool {
		got, err := c.GetObjective(context.Background(), obj.ID)
		return err == nil && got.Status == "partial"
	})

	// Find the failed stream via the run snapshot.
	snap, err := c.ObjectiveRunSnapshot(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("ObjectiveRunSnapshot: %v", err)
	}
	var failedStreamID string
	for _, s := range snap.Streams {
		if s.Status == domain.StreamStatusFailed {
			failedStreamID = s.StreamID
		}
	}
	if failedStreamID == "" {
		t.Fatal("expected one failed stream in snapshot")
	}

	// Retry with guidance through the run-centric boundary.
	if _, err := c.RunCommand(context.Background(), snap.RunID, domain.Command{
		Kind:     domain.CommandRetry,
		StreamID: failedStreamID,
		Guidance: "fix the build",
	}); err != nil {
		t.Fatalf("RunCommand(retry): %v", err)
	}

	// Wait for stream to reach "merged" — proves merge actually completed,
	// not just enqueued. The objective should follow (partial -> completed).
	waitForCondition(t, 30*time.Second, func() bool {
		p := mustGetJSON[planWithStreams](t, baseURL+"/objectives/"+obj.ID+"/plan")
		return len(p.Streams) == 1 && p.Streams[0].Status == domain.StreamStatusMerged
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

	// t.Chdir is test-scoped: it changes cwd for the current test and
	// automatically restores it when the test finishes, avoiding races
	// between concurrent test goroutines that share the process-wide cwd.
	t.Chdir(repo)

	runCmd := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
		}
	}

	runCmd("git", "init", "-q", "-b", config.DefaultBaseBranch)
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test repo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	runCmd("git", "add", "README.md")
	runCmd("git", "commit", "-q", "-m", "init")

	return func() { /* t.Chdir restores cwd automatically */ }
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
	cfg.Sandbox.Provider = "local"
	cfg.Agents.Runtime = "claude-code"
	cfg.RuntimeAuth = config.RuntimeAuthConfig{Mode: "tack", Runtime: "claude-code", Provider: "anthropic", Method: "api_key", CredentialRef: "anthropic"}
	cfg.QualityGates = append([]string(nil), qualityGates...)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	registerDaemonTestProject(t, d, cwd)

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
		// Brief drain: give background goroutines (sandbox cleanup, event
		// publishing) time to finish before the next test changes cwd or
		// reuses the port.
		time.Sleep(100 * time.Millisecond)
	}
}

func registerDaemonTestProject(t *testing.T, d *Daemon, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".tack"), 0o755); err != nil {
		t.Fatalf("MkdirAll .tack: %v", err)
	}
	configPath := filepath.Join(root, ".tack", "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if err := os.WriteFile(configPath, []byte(testClaudeCodeConfigYAML()), 0o644); err != nil {
			t.Fatalf("WriteFile config: %v", err)
		}
	}
	project := &domain.Project{ID: "test-project", Name: filepath.Base(root), RootPath: root, ConfigPath: configPath}
	if err := d.projectStore.Upsert(context.Background(), project); err != nil {
		t.Fatalf("register daemon test project: %v", err)
	}
	d.projectCtxs.Invalidate(project.ID)
}

func testClaudeCodeConfigYAML() string {
	return "sandbox:\n  provider: local\nagents:\n  runtime: claude-code\nruntime_auth:\n  mode: tack\n  runtime: claude-code\n  provider: anthropic\n  method: api_key\n  credential_ref: anthropic\n"
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
	req := authedRequest(t, http.MethodPost, url, bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
	resp, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, url, nil))
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

func authedRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	token, err := daemonauth.Load()
	if err != nil {
		t.Fatalf("load daemon token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestDaemonRestart_RediscoversLocalSandboxesAndCompletesObjective exercises the
// full restart path: daemon 1 creates an objective that spawns sandboxes, then
// shuts down mid-execution. Daemon 2 boots with the same database and worktree
// directory, calls Rediscover to repopulate sandboxes, recovers the running
// execution, and drives it to completion. Without working rediscovery the
// cleanup path would leak worktrees (CleanupObjective uses List by label).
