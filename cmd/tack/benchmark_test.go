package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syndg/tack/internal/benchmark"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
)

func resetBenchmarkTestState() {
	benchmarkRunMode = ""
	benchmarkRunBlueprint = ""
	benchmarkPrepareSource = ""
	benchmarkPrepareWorkspace = ""
	benchmarkPrepareBaseline = ""
	benchmarkExecuteWorkspace = ""
	benchmarkReportWrite = false
	benchmarkReportOut = ""
	benchmarkPlanQualityGateWait = 10 * time.Minute
	benchmarkPlanQualityGatePollInterval = 2 * time.Second
	daemonURL = "http://localhost:9800"
	projectID = ""
}

func createBenchmarkFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, contents string) {
		t.Helper()
		fullPath := filepath.Join(repoDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}

	run("init")
	run("config", "user.name", "Bench Tester")
	run("config", "user.email", "bench@example.com")
	write("go.mod", "module example.com/benchrepo\n\ngo 1.24\n")
	write("README.md", "one\n")
	write("pkg/gui/controllers/undo.go", "package controllers\n\nfunc Undo() string { return \"ok\" }\n")
	write("pkg/gui/controllers/undo_test.go", "package controllers\n\nimport \"testing\"\n\nfunc TestUndo(t *testing.T) {\n\tif Undo() != \"ok\" {\n\t\tt.Fatal(\"unexpected undo result\")\n\t}\n}\n")
	write("pkg/commands/git_commands/reflog.go", "package git_commands\n\nfunc ReflogCommand() string { return \"ok\" }\n")
	write("cmd/integration_test/main.go", "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tif len(os.Args) < 3 {\n\t\tfmt.Fprintln(os.Stderr, \"missing integration args\")\n\t\tos.Exit(1)\n\t}\n}\n")
	run("add", ".")
	run("commit", "-m", "one")
	baseline := run("rev-parse", "HEAD")
	write("README.md", "two\n")
	run("add", "README.md")
	run("commit", "-m", "two")
	return repoDir, baseline
}

func TestBenchmarkListShowsBuiltInBenchmarks(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "list"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "lazygit.undo-basic-commit-checkout") {
		t.Fatalf("output missing benchmark id:\n%s", output)
	}
	if !strings.Contains(output, "Lazygit undo commit and checkout") {
		t.Fatalf("output missing benchmark name:\n%s", output)
	}
	for _, needle := range []string{
		"feature-resurrection",
		"baseline,go,tui",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkShowDisplaysBenchmarkDetails(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "show", "lazygit.undo-basic-commit-checkout"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"ID: lazygit.undo-basic-commit-checkout",
		"Name: Lazygit undo commit and checkout",
		"Repo: lazygit",
		"Repo URL: https://github.com/jesseduffield/lazygit.git",
		"Default branch: master",
		"Family: feature-resurrection",
		"Tags: baseline, go, tui",
		"Readiness: ready",
		"Baseline: 43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
		"Feature range: 4065175a5811d688adc65b4b974f6fced0cdba67",
		"Context policy: repo-only-no-history",
		"Validation: go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v",
		"Prompt: Match lazygit's canonical reflog undo slice for recent plain commit and checkout actions.",
		"Outstanding: none",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkRunbookDisplaysManualPrepSteps(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "runbook", "lazygit.undo-basic-commit-checkout"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Readiness: ready",
		"Step 1: clone https://github.com/jesseduffield/lazygit.git into a clean benchmark workspace",
		"Step 2: check out baseline 43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
		"Step 5: validate with go test ./pkg/integration/clients -run 'TestIntegration/undo/undo_commit$' -count=1 -v && go test ./pkg/integration/clients -run 'TestIntegration/reflog/checkout$' -count=1 -v",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkReadyDisplaysOutstandingRequirements(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "ready", "lazygit.undo-basic-commit-checkout"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Readiness: ready",
		"Outstanding: none",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkPrepareCreatesWorkspaceAtBaseline(t *testing.T) {
	resetBenchmarkTestState()
	repoDir, baseline := createBenchmarkFixtureRepo(t)

	dataDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "prepared")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "prepare", "lazygit.undo-basic-commit-checkout", "--source", repoDir, "--workspace", workspace, "--baseline", baseline})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("prepare Execute: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, needle := range []string{
		"Prepared benchmark workspace",
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Baseline: " + baseline,
		"Workspace: " + workspace,
		"Preflight: passed (2 gates)",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		t.Fatalf("prepared workspace missing .git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".tack", "config.yaml")); err != nil {
		t.Fatalf("prepared workspace missing .tack/config.yaml: %v", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(workspace, ".tack", "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile benchmark config: %v", err)
	}
	if !strings.Contains(string(configBytes), "base_branch: benchmark-base") {
		t.Fatalf("benchmark config missing default branch:\n%s", string(configBytes))
	}
	for _, needle := range []string{
		"quality_gates:",
		"go test ./pkg/gui/controllers -count=1",
		"go test ./pkg/commands/git_commands -count=1",
	} {
		if !strings.Contains(string(configBytes), needle) {
			t.Fatalf("benchmark config missing %q:\n%s", needle, string(configBytes))
		}
	}
	preflightBytes, err := os.ReadFile(filepath.Join(workspace, ".tack", "benchmark-preflight.json"))
	if err != nil {
		t.Fatalf("ReadFile benchmark preflight: %v", err)
	}
	if !strings.Contains(string(preflightBytes), `"passed": true`) {
		t.Fatalf("benchmark preflight report missing pass flag:\n%s", string(preflightBytes))
	}
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = workspace
	headOut, err := headCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v\n%s", err, string(headOut))
	}
	if got := strings.TrimSpace(string(headOut)); got != baseline {
		t.Fatalf("workspace HEAD = %s, want %s", got, baseline)
	}
	baseCmd := exec.Command("git", "rev-parse", "benchmark-base")
	baseCmd.Dir = workspace
	baseOut, err := baseCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse benchmark-base failed: %v\n%s", err, string(baseOut))
	}
	if got := strings.TrimSpace(string(baseOut)); got != baseline {
		t.Fatalf("benchmark-base = %s, want %s", got, baseline)
	}
	originCmd := exec.Command("git", "remote", "get-url", "origin")
	originCmd.Dir = workspace
	originOut, err := originCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git remote get-url origin failed: %v\n%s", err, string(originOut))
	}
	if got := strings.TrimSpace(string(originOut)); got != "https://github.com/jesseduffield/lazygit.git" {
		t.Fatalf("workspace origin = %s, want canonical repo URL", got)
	}
}

func TestBenchmarkPrepareFailsFastOnInvalidBaselinePreflight(t *testing.T) {
	resetBenchmarkTestState()
	repoDir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("config", "user.name", "Bench Tester")
	run("config", "user.email", "bench@example.com")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("broken\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "broken")
	baseline := run("rev-parse", "HEAD")

	dataDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "prepared")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "prepare", "lazygit.undo-basic-commit-checkout", "--source", repoDir, "--workspace", workspace, "--baseline", baseline})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected preflight failure\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), "benchmark preflight failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBenchmarkExecuteStartsObjectiveAndPersistsLinks(t *testing.T) {
	resetBenchmarkTestState()
	workspace := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	git("init")
	git("config", "user.name", "Bench Tester")
	git("config", "user.email", "bench@example.com")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("bench\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	git("add", "README.md")
	git("commit", "-m", "init")
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = workspace
	headOut, err := headCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v\n%s", err, string(headOut))
	}
	head := strings.TrimSpace(string(headOut))
	if err := os.MkdirAll(filepath.Join(workspace, ".tack"), 0o755); err != nil {
		t.Fatalf("MkdirAll .tack: %v", err)
	}
	preflightRaw, err := json.MarshalIndent(benchmark.PreflightReport{
		BenchmarkID:  "lazygit.undo-basic-commit-checkout",
		Workspace:    workspace,
		Baseline:     "43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
		Head:         head,
		QualityGates: []string{"go test ./pkg/gui/controllers -count=1", "go test ./pkg/commands/git_commands -count=1"},
		Clean:        true,
		Passed:       true,
		ValidatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		t.Fatalf("Marshal preflight: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".tack", "benchmark-preflight.json"), preflightRaw, 0o644); err != nil {
		t.Fatalf("WriteFile benchmark preflight: %v", err)
	}

	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	var registered bool
	var created bool
	var qualityGatesUpdated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/projects/resolve"):
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/objectives/obj-1/plan":
			resp := struct {
				Plan    domain.Plan     `json:"plan"`
				Streams []domain.Stream `json:"streams"`
			}{
				Plan: domain.Plan{
					ID:          "plan-1",
					ProjectID:   "proj-1",
					ObjectiveID: "obj-1",
				},
				Streams: []domain.Stream{
					{Title: "Narrow reflog undo core to plain commit and checkout", Card: &domain.StreamCard{}},
					{Title: "Exercise supported and unsupported undo flows in integration tests", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
					{Title: "Update user-facing docs and copy for the benchmark slice", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && r.URL.Path == "/objectives/obj-1/run":
			snap := domain.Snapshot{RunID: "run-1", ProjectID: "proj-1", ObjectiveID: "obj-1", Status: domain.RunStatusActive}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
		case r.Method == http.MethodPost && r.URL.Path == "/plans/plan-1/quality-gates":
			qualityGatesUpdated = true
			var req struct {
				QualityGates []string `json:"quality_gates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode quality gates request: %v", err)
			}
			if len(req.QualityGates) != 2 || req.QualityGates[0] != "go test ./pkg/gui/controllers -count=1" || req.QualityGates[1] != "go test ./pkg/commands/git_commands -count=1" {
				t.Fatalf("unexpected benchmark quality gates: %v", req.QualityGates)
			}
			plan := domain.Plan{
				ID:           "plan-1",
				ProjectID:    "proj-1",
				ObjectiveID:  "obj-1",
				QualityGates: req.QualityGates,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(plan)
		case r.Method == http.MethodPost && r.URL.Path == "/projects/register":
			registered = true
			var project domain.Project
			project.ID = "proj-1"
			project.RootPath = workspace
			project.ConfigPath = filepath.Join(workspace, ".tack", "config.yaml")
			project.CreatedAt = time.Now()
			project.UpdatedAt = time.Now()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(project)
		case r.Method == http.MethodPost && r.URL.Path == "/objectives":
			created = true
			if got := r.Header.Get("X-Tack-Project-ID"); got != "proj-1" {
				t.Fatalf("X-Tack-Project-ID = %q, want proj-1", got)
			}
			var req struct {
				Description string `json:"description"`
				Blueprint   string `json:"blueprint,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode objective request: %v", err)
			}
			if req.Description == "" {
				t.Fatal("expected benchmark prompt to be used as objective description")
			}
			obj := domain.Objective{
				ID:          "obj-1",
				ProjectID:   "proj-1",
				Description: req.Description,
				Status:      domain.ObjectiveStatusPlanning,
				Blueprint:   "benchmark-baseline",
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(obj)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	runID := strings.TrimSpace(strings.SplitN(runOutput[strings.Index(runOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"--daemon-url", server.URL, "benchmark", "execute", runID, "--workspace", workspace})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute Execute: %v\nstderr: %s", err, stderr.String())
	}
	if !registered {
		t.Fatal("expected project registration")
	}
	if !created {
		t.Fatal("expected objective creation")
	}
	if !qualityGatesUpdated {
		t.Fatal("expected benchmark quality gates override")
	}
	output := stdout.String()
	for _, needle := range []string{
		"Started benchmark execution",
		"Run: " + runID,
		"Project: proj-1",
		"Objective: obj-1",
		"Workspace: " + workspace,
		"Status: executing",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "show-run", runID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}
	showOutput := stdout.String()
	for _, needle := range []string{
		"Project: proj-1",
		"Objective: obj-1",
		"Workspace: " + workspace,
		"Status: executing",
	} {
		if !strings.Contains(showOutput, needle) {
			t.Fatalf("show-run missing %q:\n%s", needle, showOutput)
		}
	}
}

func TestBenchmarkShowRunUsesConfiguredDaemonListenAndResyncsTransientFailure(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/objectives/obj-1/run":
			if got := r.Header.Get("X-Tack-Project-ID"); got != "proj-1" {
				t.Fatalf("X-Tack-Project-ID = %q, want proj-1", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(domain.Snapshot{RunID: "daemon-run-1", ProjectID: "proj-1", ObjectiveID: "obj-1", Status: domain.RunStatusCompleted})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	listenAddr := strings.TrimPrefix(server.URL, "http://")
	if err := os.WriteFile(configPath, []byte("daemon:\n  listen: "+listenAddr+"\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	run, ok := benchmark.PrepareRun("lazygit.undo-basic-commit-checkout", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	run.ID = "bench-run-1"
	run.RunID = "stale-run"
	run.ProjectID = "proj-1"
	run.ObjectiveID = "obj-1"
	run.Status = "failed"
	run.StatusReason = "underlying objective run not found"
	if err := benchmark.SaveRun(dataDir, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "show-run", run.ID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, needle := range []string{"Run: bench-run-1", "Daemon run: daemon-run-1", "Status: completed"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("show-run missing %q:\n%s", needle, output)
		}
	}
	updated, found, err := benchmark.FindRun(dataDir, run.ID)
	if err != nil {
		t.Fatalf("FindRun: %v", err)
	}
	if !found {
		t.Fatal("expected saved benchmark run")
	}
	if updated.Status != "completed" || updated.RunID != "daemon-run-1" {
		t.Fatalf("updated run = %+v", updated)
	}
	if updated.StatusReason != "" {
		t.Fatalf("expected cleared status reason, got %q", updated.StatusReason)
	}
}

func TestBenchmarkShowRunResyncsPartialRun(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/objectives/obj-1/run" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(domain.Snapshot{RunID: "daemon-run-1", ProjectID: "proj-1", ObjectiveID: "obj-1", Status: domain.RunStatusCompleted})
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	listenAddr := strings.TrimPrefix(server.URL, "http://")
	if err := os.WriteFile(configPath, []byte("daemon:\n  listen: "+listenAddr+"\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	run, ok := benchmark.PrepareRun("lazygit.undo-basic-commit-checkout", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	run.ID = "bench-partial"
	run.RunID = "daemon-run-1"
	run.ProjectID = "proj-1"
	run.ObjectiveID = "obj-1"
	run.Status = "partial"
	if err := benchmark.SaveRun(dataDir, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "show-run", run.ID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}
	updated, found, err := benchmark.FindRun(dataDir, run.ID)
	if err != nil {
		t.Fatalf("FindRun: %v", err)
	}
	if !found {
		t.Fatal("expected saved benchmark run")
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
}

func TestBenchmarkRunCreatesPlannedRun(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout", "--mode", "planning", "--blueprint", "major-feature-interactive"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"Created benchmark run",
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Status: planned",
		"Mode: planning",
		"Blueprint: major-feature-interactive",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkShowUnknownReturnsClearError(t *testing.T) {
	resetBenchmarkTestState()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "show", "missing.benchmark"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for unknown benchmark\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), `unknown benchmark "missing.benchmark"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBenchmarkRunsListsRecordedRuns(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "runs"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("runs Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"lazygit.undo-basic-commit-checkout",
		"planned",
		"auto",
		"benchmark-baseline",
		"pending",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkShowRunDisplaysRecordedRun(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	const prefix = "Created benchmark run "
	idx := strings.Index(runOutput, prefix)
	if idx == -1 {
		t.Fatalf("missing run creation prefix in output:\n%s", runOutput)
	}
	runIDLine := runOutput[idx+len(prefix):]
	runID := strings.TrimSpace(strings.SplitN(runIDLine, "\n", 2)[0])
	if runID == "" {
		t.Fatalf("empty run id in output:\n%s", runOutput)
	}

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "show-run", runID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"Run: " + runID,
		"Daemon run: ",
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Status: planned",
		"Reason: ",
		"Repo: lazygit",
		"Readiness snapshot: ready",
		"Baseline: 43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
		"Context policy: repo-only-no-history",
		"Requested mode: auto",
		"Effective mode: auto",
		"Blueprint: benchmark-baseline",
		"Version: 0.1.0",
		"Created:",
		"Prompt snapshot: Match lazygit's canonical reflog undo slice for recent plain commit and checkout actions.",
		"Keep the end-to-end validation entrypoints `undo/undo_commit` and `reflog/checkout` intact:",
		"Score: pending",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkShowRunUnknownReturnsClearError(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "show-run", "missing-run"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for unknown benchmark run\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), `unknown benchmark run "missing-run"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBenchmarkReportDisplaysTelemetry(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	projectRoot := t.TempDir()
	project := &domain.Project{Name: "bench", RootPath: projectRoot, ConfigPath: projectRoot}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ID: "obj-report", ProjectID: project.ID, Description: "report objective", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	planStore := db.NewPlanStore(database.Conn())
	plan := &domain.Plan{ID: "plan-report", ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.PlanStatusCompleted, QualityGates: []string{"go test ./pkg/gui/... -count=1"}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	streamStore := db.NewStreamStore(database.Conn())
	stream := &domain.Stream{ID: "stream-report", ProjectID: project.ID, PlanID: plan.ID, Title: "Stream report", Status: domain.StreamStatusMerged}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}
	runStore := db.NewRunStore(database.Conn())
	daemonRun := &domain.Run{ID: "daemon-report", ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.RunStatusCompleted}
	if err := runStore.Create(ctx, daemonRun); err != nil {
		t.Fatalf("Create run: %v", err)
	}
	execStore := db.NewExecutionStore(database.Conn())
	now := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	topExec := &blueprint.Execution{ID: "exec-top-report", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, CurrentStep: "execute", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now, UpdatedAt: now.Add(5 * time.Minute)}
	if err := execStore.Create(ctx, topExec); err != nil {
		t.Fatalf("Create top exec: %v", err)
	}
	subExec := &blueprint.Execution{ID: "exec-sub-report", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: stream.ID, CurrentStep: "merge", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(4 * time.Minute)}
	if err := execStore.Create(ctx, subExec); err != nil {
		t.Fatalf("Create sub exec: %v", err)
	}
	agentStore := db.NewAgentStore(database.Conn())
	for _, session := range []*domain.AgentSession{
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: stream.ID, Role: domain.AgentRoleBuilder, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: stream.ID, Role: domain.AgentRoleReviewer, Status: "completed"},
	} {
		if err := agentStore.Create(ctx, session); err != nil {
			t.Fatalf("Create agent session: %v", err)
		}
	}
	mergeStore := db.NewMergeQueueStore(database.Conn())
	entry := &domain.MergeEntry{ID: "merge-report", ProjectID: project.ID, StreamID: stream.ID, PlanID: plan.ID, ObjectiveID: objective.ID, Branch: "branch", Status: domain.MergeStatusMerged, CreatedAt: now.Add(4 * time.Minute).Unix(), UpdatedAt: now.Add(5 * time.Minute).Unix()}
	if err := mergeStore.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue merge entry: %v", err)
	}
	if err := mergeStore.UpdateStatus(ctx, entry.ID, entry.Status, 0, "", ""); err != nil {
		t.Fatalf("Update merge entry: %v", err)
	}

	run, ok := benchmark.PrepareRun("lazygit.command-log-nav-keybindings", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	run.ID = "bench-report"
	run.RunID = daemonRun.ID
	run.ProjectID = project.ID
	run.ObjectiveID = objective.ID
	run.Status = "completed"
	if err := benchmark.SaveRun(dataDir, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "report", run.ID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("report Execute: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, needle := range []string{
		"# Benchmark Telemetry",
		"- Benchmark run: `" + run.ID + "`",
		"- Streams: `1 total`, `1 merged`, `0 failed`, `0 non-terminal`",
		"- Builder sessions: `1`",
		"- Reviewer sessions: `1`",
		"- Merge attempts: `1`",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkReportWriteCreatesCanonicalMarkdownFile(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".tack"), 0o755); err != nil {
		t.Fatalf("MkdirAll .tack: %v", err)
	}
	configPath := filepath.Join(projectRoot, ".tack", "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(oldwd); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	project := &domain.Project{Name: "bench", RootPath: projectRoot, ConfigPath: configPath}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ID: "obj-write", ProjectID: project.ID, Description: "write objective", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	planStore := db.NewPlanStore(database.Conn())
	plan := &domain.Plan{ID: "plan-write", ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.PlanStatusCompleted, QualityGates: []string{"go test ./pkg/gui/... -count=1"}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	streamStore := db.NewStreamStore(database.Conn())
	stream := &domain.Stream{ID: "stream-write", ProjectID: project.ID, PlanID: plan.ID, Title: "Stream write", Status: domain.StreamStatusMerged}
	if err := streamStore.Create(ctx, stream); err != nil {
		t.Fatalf("Create stream: %v", err)
	}
	runStore := db.NewRunStore(database.Conn())
	daemonRun := &domain.Run{ID: "daemon-write", ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.RunStatusCompleted}
	if err := runStore.Create(ctx, daemonRun); err != nil {
		t.Fatalf("Create run: %v", err)
	}
	execStore := db.NewExecutionStore(database.Conn())
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	topExec := &blueprint.Execution{ID: "exec-top-write", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, CurrentStep: "execute", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now, UpdatedAt: now.Add(2 * time.Minute)}
	if err := execStore.Create(ctx, topExec); err != nil {
		t.Fatalf("Create top exec: %v", err)
	}
	subExec := &blueprint.Execution{ID: "exec-sub-write", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: stream.ID, CurrentStep: "merge", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now.Add(10 * time.Second), UpdatedAt: now.Add(90 * time.Second)}
	if err := execStore.Create(ctx, subExec); err != nil {
		t.Fatalf("Create sub exec: %v", err)
	}

	run, ok := benchmark.PrepareRun("lazygit.command-log-nav-keybindings", "", "")
	if !ok {
		t.Fatal("expected built-in benchmark spec")
	}
	run.ID = "bench-write"
	run.RunID = daemonRun.ID
	run.ProjectID = project.ID
	run.ObjectiveID = objective.ID
	run.Status = "completed"
	run.CreatedAt = now.Format(time.RFC3339)
	if err := benchmark.SaveRun(dataDir, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "report", run.ID, "--write"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("report --write Execute: %v\nstderr: %s", err, stderr.String())
	}
	expectedPath := filepath.Join(projectRoot, "docs", "benchmarks", "2026-04-13-lazygit-command-log-nav-keybindings-bench-write.md")
	if !strings.Contains(stdout.String(), expectedPath) {
		t.Fatalf("stdout missing path %q:\n%s", expectedPath, stdout.String())
	}
	raw, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile report: %v", err)
	}
	content := string(raw)
	for _, needle := range []string{
		"# Benchmark Telemetry",
		"- Benchmark run: `bench-write`",
		"- Streams: `1 total`, `1 merged`, `0 failed`, `0 non-terminal`",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("report missing %q:\n%s", needle, content)
		}
	}
}

func TestBenchmarkExecuteWaitsForDelayedPlanBeforeApplyingQualityGates(t *testing.T) {
	resetBenchmarkTestState()
	repoDir, baseline := createBenchmarkFixtureRepo(t)
	workspace := filepath.Join(t.TempDir(), "prepared")
	if err := exec.Command("git", "clone", repoDir, workspace).Run(); err != nil {
		t.Fatalf("clone fixture repo: %v", err)
	}
	checkout := exec.Command("git", "checkout", baseline)
	checkout.Dir = workspace
	if out, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("checkout baseline: %v\n%s", err, string(out))
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".tack"), 0o755); err != nil {
		t.Fatalf("MkdirAll .tack: %v", err)
	}
	preflightRaw, err := json.MarshalIndent(benchmark.PreflightReport{
		BenchmarkID:  "lazygit.undo-basic-commit-checkout",
		Workspace:    workspace,
		Baseline:     "43106b6c7fbe8c69cebb02f8fc80cb060faddeee",
		Head:         baseline,
		QualityGates: []string{"go test ./pkg/gui/controllers -count=1", "go test ./pkg/commands/git_commands -count=1"},
		Clean:        true,
		Passed:       true,
		ValidatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		t.Fatalf("Marshal preflight: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".tack", "benchmark-preflight.json"), preflightRaw, 0o644); err != nil {
		t.Fatalf("WriteFile benchmark preflight: %v", err)
	}

	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)
	benchmarkPlanQualityGateWait = 5 * time.Second
	benchmarkPlanQualityGatePollInterval = 10 * time.Millisecond

	var planReads atomic.Int32
	var qualityGatesUpdated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/projects/resolve"):
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/objectives/obj-delayed/plan":
			if planReads.Add(1) < 3 {
				http.Error(w, `{"error":"plan not found"}`, http.StatusNotFound)
				return
			}
			resp := struct {
				Plan    domain.Plan     `json:"plan"`
				Streams []domain.Stream `json:"streams"`
			}{
				Plan: domain.Plan{ID: "plan-delayed", ProjectID: "proj-delayed", ObjectiveID: "obj-delayed"},
				Streams: []domain.Stream{
					{Title: "Narrow reflog undo core to plain commit and checkout", Card: &domain.StreamCard{}},
					{Title: "Exercise supported and unsupported undo flows in integration tests", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
					{Title: "Update user-facing docs and copy for the benchmark slice", Card: &domain.StreamCard{BlockedBy: []string{"Narrow reflog undo core to plain commit and checkout"}}},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && r.URL.Path == "/objectives/obj-delayed/run":
			snap := domain.Snapshot{RunID: "run-delayed", ProjectID: "proj-delayed", ObjectiveID: "obj-delayed", Status: domain.RunStatusActive}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
		case r.Method == http.MethodPost && r.URL.Path == "/plans/plan-delayed/quality-gates":
			qualityGatesUpdated.Store(true)
			var req struct {
				QualityGates []string `json:"quality_gates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode quality gates request: %v", err)
			}
			if len(req.QualityGates) != 2 || req.QualityGates[0] != "go test ./pkg/gui/controllers -count=1" || req.QualityGates[1] != "go test ./pkg/commands/git_commands -count=1" {
				t.Fatalf("unexpected benchmark quality gates: %v", req.QualityGates)
			}
			plan := domain.Plan{ID: "plan-delayed", ProjectID: "proj-delayed", ObjectiveID: "obj-delayed", QualityGates: req.QualityGates}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(plan)
		case r.Method == http.MethodPost && r.URL.Path == "/projects/register":
			project := domain.Project{ID: "proj-delayed", RootPath: workspace, ConfigPath: filepath.Join(workspace, ".tack", "config.yaml"), CreatedAt: time.Now(), UpdatedAt: time.Now()}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(project)
		case r.Method == http.MethodPost && r.URL.Path == "/objectives":
			obj := domain.Objective{ID: "obj-delayed", ProjectID: "proj-delayed", Description: "delayed plan", Status: domain.ObjectiveStatusPlanning, Blueprint: "benchmark-baseline", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(obj)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	runID := strings.TrimSpace(strings.SplitN(runOutput[strings.Index(runOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"--daemon-url", server.URL, "benchmark", "execute", runID, "--workspace", workspace})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute Execute: %v\nstderr: %s", err, stderr.String())
	}
	if !qualityGatesUpdated.Load() {
		t.Fatal("expected delayed benchmark quality gates override")
	}
	if got := planReads.Load(); got < 3 {
		t.Fatalf("expected repeated plan polling, got %d reads", got)
	}
}

func TestBenchmarkCompareDisplaysTwoRuns(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)

	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first run Execute: %v\nstderr: %s", err, stderr.String())
	}
	firstOutput := stdout.String()
	firstID := strings.TrimSpace(strings.SplitN(firstOutput[strings.Index(firstOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second run Execute: %v\nstderr: %s", err, stderr.String())
	}
	secondOutput := stdout.String()
	secondID := strings.TrimSpace(strings.SplitN(secondOutput[strings.Index(secondOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "compare", firstID, secondID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("compare Execute: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	for _, needle := range []string{
		"A: " + firstID,
		"B: " + secondID,
		"Benchmark: lazygit.undo-basic-commit-checkout",
		"Readiness: ready -> ready",
		"Status: planned -> planned",
		"Mode: auto -> auto",
		"Blueprint: benchmark-baseline -> benchmark-baseline",
		"Version: 0.1.0 -> 0.1.0",
		"Score: pending -> pending",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkRunStoresRequestedModeAndBlueprint(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout", "--mode", "guided", "--blueprint", "standard"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	runID := strings.TrimSpace(strings.SplitN(runOutput[strings.Index(runOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "show-run", runID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, needle := range []string{
		"Requested mode: guided",
		"Effective mode: guided",
		"Blueprint: standard",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("output missing %q:\n%s", needle, output)
		}
	}
}

func TestBenchmarkCompareUnknownRunReturnsClearError(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	runID := strings.TrimSpace(strings.SplitN(runOutput[strings.Index(runOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "compare", runID, "missing-run"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected compare error for unknown run\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), `unknown benchmark run "missing-run"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBenchmarkSupersedeMarksRunSuperseded(t *testing.T) {
	resetBenchmarkTestState()
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("daemon:\n  data_dir: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"benchmark", "run", "lazygit.undo-basic-commit-checkout"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run Execute: %v\nstderr: %s", err, stderr.String())
	}
	runOutput := stdout.String()
	runID := strings.TrimSpace(strings.SplitN(runOutput[strings.Index(runOutput, "Created benchmark run ")+len("Created benchmark run "):], "\n", 2)[0])

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "supersede", runID, "--reason", "obsolete standard-merge benchmark"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("supersede Execute: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Superseded benchmark run "+runID) {
		t.Fatalf("unexpected supersede output:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	rootCmd.SetArgs([]string{"benchmark", "show-run", runID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("show-run Execute: %v\nstderr: %s", err, stderr.String())
	}
	output := stdout.String()
	for _, needle := range []string{
		"Status: superseded",
		"Reason: obsolete standard-merge benchmark",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("show-run missing %q:\n%s", needle, output)
		}
	}
}
