package dispatch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
)

func TestHandleDeterministic_UnknownAction(t *testing.T) {
	env := setupDispatchEnv(t)

	h := &Handlers{
		scheduler:  env.scheduler,
		plans:      env.plans,
		streams:    env.streams,
		objectives: env.objectives,
		executions: env.executions,
		agents:     env.agents,
		eventBus:   env.eventBus,
		lifecycle:  env.lifecycle,
		baseBranch: "main",
		logger:     env.logger,
	}

	exec := &blueprint.Execution{ID: "exec-1", ObjectiveID: "obj-1"}
	step := &blueprint.Step{ID: "step-1", Type: blueprint.StepTypeDeterministic, Action: "nonexistent_action"}

	result, err := h.HandleDeterministic(context.Background(), exec, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if result.Error == "" {
		t.Fatal("expected error message for unknown action")
	}
}

func TestHandleDeterministic_DispatchStreams(t *testing.T) {
	env := setupDispatchEnv(t)

	env.createObjective(t, "obj-dispatch", domain.ObjectiveStatusExecuting)
	env.createPlan(t, "plan-dispatch", "obj-dispatch", []string{"s1", "s2"})

	h := &Handlers{
		scheduler:  env.scheduler,
		plans:      env.plans,
		streams:    env.streams,
		objectives: env.objectives,
		executions: env.executions,
		agents:     env.agents,
		eventBus:   env.eventBus,
		lifecycle:  env.lifecycle,
		baseBranch: "main",
		logger:     env.logger,
	}

	exec := &blueprint.Execution{ID: "exec-2", ObjectiveID: "obj-dispatch"}
	step := &blueprint.Step{ID: "step-dispatch", Type: blueprint.StepTypeDeterministic, Action: "dispatch_streams"}

	result, err := h.HandleDeterministic(context.Background(), exec, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("expected completed, got %s (error: %s)", result.Status, result.Error)
	}
}

type handlersTestMergeHelper struct{ mergerID string }

func (m *handlersTestMergeHelper) EnqueueStream(_ context.Context, _ string) error { return nil }
func (m *handlersTestMergeHelper) MergerSandboxID(_ string) string                 { return m.mergerID }
func (m *handlersTestMergeHelper) ResetMergingEntries(_ context.Context, _ string) {}

type handlersTestSandboxProvider struct{ sb sandbox.Sandbox }

func (p *handlersTestSandboxProvider) Create(_ context.Context, _ sandbox.CreateOpts) (sandbox.Sandbox, error) {
	return p.sb, nil
}
func (p *handlersTestSandboxProvider) Get(_ context.Context, _ string) (sandbox.Sandbox, error) {
	return p.sb, nil
}
func (p *handlersTestSandboxProvider) List(_ context.Context, _ map[string]string) ([]sandbox.Sandbox, error) {
	return []sandbox.Sandbox{p.sb}, nil
}
func (p *handlersTestSandboxProvider) Delete(_ context.Context, _ string) error { return nil }

type handlersTestSandbox struct {
	id     string
	execFn func(cmd string) (sandbox.ExecResult, error)
}

func (s *handlersTestSandbox) ID() string                    { return s.id }
func (s *handlersTestSandbox) Status() sandbox.SandboxStatus { return sandbox.SandboxStatusRunning }
func (s *handlersTestSandbox) Exec(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
	if s.execFn != nil {
		return s.execFn(cmd)
	}
	return sandbox.ExecResult{}, nil
}
func (s *handlersTestSandbox) ExecStreaming(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *handlersTestSandbox) Upload(_ context.Context, _ []byte, _ string) error   { return nil }
func (s *handlersTestSandbox) Download(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (s *handlersTestSandbox) Stop(_ context.Context) error                         { return nil }
func (s *handlersTestSandbox) Start(_ context.Context) error                        { return nil }
func (s *handlersTestSandbox) Health() error                                        { return nil }

type handlersTestRuntime struct {
	spawnFn func(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error)
}

func (r *handlersTestRuntime) Spawn(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	return r.spawnFn(ctx, sb, opts)
}
func (r *handlersTestRuntime) Name() string        { return "test" }
func (r *handlersTestRuntime) SupportsRPC() bool   { return false }
func (r *handlersTestRuntime) SupportsHooks() bool { return false }

type handlersTestProcess struct {
	result runtime.AgentResult
}

func (p *handlersTestProcess) Send(_ context.Context, _ runtime.AgentMessage) error { return nil }
func (p *handlersTestProcess) Output() <-chan runtime.AgentEvent {
	return make(chan runtime.AgentEvent)
}
func (p *handlersTestProcess) Wait() (runtime.AgentResult, error) { return p.result, nil }
func (p *handlersTestProcess) Kill() error                        { return nil }

func advanceStreamForHandlersTest(t *testing.T, env *dispatchTestEnv, id string, target domain.StreamStatus) {
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
		if err := env.streams.UpdateStatus(context.Background(), id, step); err != nil {
			t.Fatalf("advancing stream to %s (step %s): %v", target, step, err)
		}
	}
}

func TestSchedulerMarkCompletedPreservesMergeReady(t *testing.T) {
	env := setupDispatchEnv(t)
	env.createObjective(t, "obj-preserve-merge-ready", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-preserve-merge-ready", "obj-preserve-merge-ready", []string{"stream-1"})
	advanceStreamForHandlersTest(t, env, streams[0].ID, domain.StreamStatusMergeReady)

	scheduler := NewScheduler(env.streams, env.plans, 1, env.eventBus, nil, env.logger)
	if err := scheduler.MarkCompleted(context.Background(), streams[0].ID, "plan-preserve-merge-ready"); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	stream, err := env.streams.Get(context.Background(), streams[0].ID)
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	if stream.Status != domain.StreamStatusMergeReady {
		t.Fatalf("stream status = %s, want merge_ready", stream.Status)
	}
}

func TestCreatePR_SkipsWhenNoMergedHeadAvailable(t *testing.T) {
	env := setupDispatchEnv(t)
	env.createObjective(t, "obj-skip-pr", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-skip-pr", "obj-skip-pr", []string{"stream-1"})
	advanceStreamForHandlersTest(t, env, streams[0].ID, domain.StreamStatusFailed)

	h := &Handlers{
		plans:      env.plans,
		streams:    env.streams,
		objectives: env.objectives,
		logger:     env.logger,
	}

	result, err := h.createPR(context.Background(), &blueprint.Execution{ID: "exec-skip", ObjectiveID: "obj-skip-pr"}, &blueprint.Step{ID: "create_pr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if !strings.Contains(result.Output, "no merged head") {
		t.Fatalf("output = %q, want skip reason", result.Output)
	}
}

func TestCreatePR_UsesGitHubAPIWhenAvailable(t *testing.T) {
	env := setupDispatchEnv(t)
	env.createObjective(t, "obj-api-pr", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-api-pr", "obj-api-pr", []string{"stream-1"})
	advanceStreamForHandlersTest(t, env, streams[0].ID, domain.StreamStatusMerged)

	creds, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load creds: %v", err)
	}
	creds.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "test-token"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/repos/syndg/repo/pulls" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/syndg/repo/pull/123"}`))
	}))
	defer server.Close()

	sb := &handlersTestSandbox{
		id: "merger-sb",
		execFn: func(cmd string) (sandbox.ExecResult, error) {
			switch cmd {
			case "git remote get-url origin":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "git@github.com:syndg/repo.git\n"}, nil
			case "git rev-parse --abbrev-ref HEAD":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "tack/obj-api-pr/merge\n"}, nil
			default:
				if strings.HasPrefix(cmd, "gh pr create") {
					t.Fatalf("sandbox gh fallback should not be used")
				}
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	h := &Handlers{
		mergeProcessor:  &handlersTestMergeHelper{mergerID: sb.ID()},
		plans:           env.plans,
		streams:         env.streams,
		objectives:      env.objectives,
		sandboxProvider: &handlersTestSandboxProvider{sb: sb},
		baseBranch:      "main",
		creds:           creds,
		githubAPIBase:   server.URL,
		logger:          env.logger,
	}

	result, err := h.createPR(context.Background(), &blueprint.Execution{ID: "exec-api", ObjectiveID: "obj-api-pr"}, &blueprint.Step{ID: "create_pr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if got := strings.TrimSpace(result.Output); got != "https://github.com/syndg/repo/pull/123" {
		t.Fatalf("output = %q", got)
	}
}

func TestCreatePR_UsesRuntimeGeneratedMessages(t *testing.T) {
	env := setupDispatchEnv(t)
	env.createObjective(t, "obj-runtime-pr", domain.ObjectiveStatusExecuting)
	streams := env.createPlan(t, "plan-runtime-pr", "obj-runtime-pr", []string{"stream-1"})
	advanceStreamForHandlersTest(t, env, streams[0].ID, domain.StreamStatusMerged)

	creds, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load creds: %v", err)
	}
	creds.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "test-token"})
	creds.SetModelProvider("anthropic", credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: "test-model-key"})

	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/syndg/repo/pull/123"}`))
	}))
	defer server.Close()

	sb := &handlersTestSandbox{
		id: "merger-sb",
		execFn: func(cmd string) (sandbox.ExecResult, error) {
			switch cmd {
			case "git remote get-url origin":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "git@github.com:syndg/repo.git\n"}, nil
			case "git rev-parse --abbrev-ref HEAD":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "tack/obj-runtime-pr/merge\n"}, nil
			case "git diff --name-only origin/main...HEAD":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "README.md\ndocs/guide.md\n"}, nil
			case "git diff --stat origin/main...HEAD":
				return sandbox.ExecResult{ExitCode: 0, Stdout: " README.md | 10 +++++\n"}, nil
			case "git diff --unified=1 origin/main...HEAD":
				return sandbox.ExecResult{ExitCode: 0, Stdout: "diff --git a/README.md b/README.md\n+hello\n"}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	h := &Handlers{
		mergeProcessor: &handlersTestMergeHelper{mergerID: sb.ID()},
		agentRuntime: &handlersTestRuntime{spawnFn: func(_ context.Context, _ sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
			if opts.Model != "deterministic-model" {
				t.Fatalf("opts.Model = %q, want deterministic-model", opts.Model)
			}
			if !strings.Contains(opts.Overlay, "README.md") {
				t.Fatalf("overlay should include diff context, got %q", opts.Overlay)
			}
			return &handlersTestProcess{result: runtime.AgentResult{Success: true, Summary: "done\nTACK_MESSAGES:{\"pr_title\":\"Drafted title\",\"pr_body\":\"## Summary\\n- drafted body\"}"}}, nil
		}},
		plans:           env.plans,
		streams:         env.streams,
		objectives:      env.objectives,
		sandboxProvider: &handlersTestSandboxProvider{sb: sb},
		baseBranch:      "main",
		creds:           creds,
		runtimeAuth:     config.RuntimeAuthConfig{Mode: "tack", Runtime: "claude-code", Provider: "anthropic", CredentialRef: "anthropic"},
		defaultModel:    "deterministic-model",
		githubAPIBase:   server.URL,
		logger:          env.logger,
	}

	result, err := h.createPR(context.Background(), &blueprint.Execution{ID: "exec-runtime", ObjectiveID: "obj-runtime-pr"}, &blueprint.Step{ID: "create_pr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != blueprint.StepStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if !strings.Contains(requestBody, "Drafted title") || !strings.Contains(requestBody, "drafted body") {
		t.Fatalf("request body = %s, want runtime-generated title/body", requestBody)
	}
}

func TestGeneratedMessagesFromSource(t *testing.T) {
	exec := &blueprint.Execution{
		StepStates: map[string]*blueprint.StepState{
			"fix": {
				StepID: "fix",
				Metadata: map[string]string{
					"pr_title": "Fix flaky dispatch flow",
					"pr_body":  "## Summary\n- stabilize dispatch flow",
				},
			},
		},
	}

	msgs := generatedMessagesFromSource(exec, "fix")
	if msgs.PRTitle != "Fix flaky dispatch flow" {
		t.Fatalf("PRTitle = %q, want generated title", msgs.PRTitle)
	}
	if msgs.PRBody == "" {
		t.Fatal("expected PRBody to be populated")
	}
}
