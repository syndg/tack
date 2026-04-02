package dispatch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
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
