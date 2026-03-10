package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syndg/deck/internal/client"
	"github.com/syndg/deck/internal/config"
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
	if status.Objectives["planning"] != 1 {
		t.Fatalf("expected planning count 1, got %d", status.Objectives["planning"])
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

func TestCreateObjectiveWithOptionsPersistsBlueprintAndPlanningMode(t *testing.T) {
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
	obj, err := c.CreateObjectiveWithOptions(context.Background(), "batch objective", client.CreateObjectiveOptions{
		Blueprint: "hotfix",
		Auto:      true,
	})
	if err != nil {
		t.Fatalf("CreateObjectiveWithOptions: %v", err)
	}
	if obj.Blueprint != "hotfix" {
		t.Fatalf("blueprint = %q, want hotfix", obj.Blueprint)
	}
	if obj.PlanningMode != "batch" {
		t.Fatalf("planning_mode = %q, want batch", obj.PlanningMode)
	}

	got, err := c.GetObjective(context.Background(), obj.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.PlanningMode != "batch" {
		t.Fatalf("persisted planning_mode = %q, want batch", got.PlanningMode)
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
