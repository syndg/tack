package preflight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/harness/blueprint"
)

type blueprints map[string]*blueprint.Blueprint

func (b blueprints) GetBlueprint(id string) (*blueprint.Blueprint, bool) {
	bp, ok := b[id]
	return bp, ok
}

func TestCheckCreatePRRequiresGitAuth(t *testing.T) {
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		GitRemote: func(context.Context, string) (string, error) { return "git@github.com:syndg/tack.git", nil },
	})

	err := checker.Check(context.Background(), "release")
	if err == nil {
		t.Fatal("expected missing git auth error")
	}
	if !strings.Contains(err.Error(), "tack auth add git") {
		t.Fatalf("error = %q, want git auth fix", err.Error())
	}
}

func TestCheckCreatePRPassesWithGitAuth(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	store.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "ghp_test"})
	server := githubValidationServer(t, http.StatusOK, true)
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials:   store,
		GitRemote:     func(context.Context, string) (string, error) { return "https://github.com/syndg/tack.git", nil },
		GitHubAPIBase: server.URL,
	})

	if err := checker.Check(context.Background(), "release"); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckCreatePRFailsWithoutOrigin(t *testing.T) {
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		GitRemote: func(context.Context, string) (string, error) { return "", errors.New("no origin") },
	})

	err := checker.Check(context.Background(), "release")
	if err == nil || !strings.Contains(err.Error(), "git remote add origin") {
		t.Fatalf("error = %v, want origin fix", err)
	}
}

func TestCheckCreatePRFailsWithoutGitHubToken(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials: store,
		GitRemote:   func(context.Context, string) (string, error) { return "https://github.com/syndg/tack.git", nil },
	})

	err = checker.Check(context.Background(), "release")
	if err == nil || !strings.Contains(err.Error(), "tack auth add git") || !strings.Contains(err.Error(), "origin=https://github.com/syndg/tack.git") {
		t.Fatalf("error = %v, want token fix and origin evidence", err)
	}
}

func TestCheckCreatePRFailsWhenGitHubDeniesAccess(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	store.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "ghp_test"})
	server := githubValidationServer(t, http.StatusForbidden, true)
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials:   store,
		GitRemote:     func(context.Context, string) (string, error) { return "https://github.com/syndg/tack.git", nil },
		GitHubAPIBase: server.URL,
	})

	err = checker.Check(context.Background(), "release")
	if err == nil || !strings.Contains(err.Error(), "status=403") || !strings.Contains(err.Error(), "authenticated_identity=tack-user") {
		t.Fatalf("error = %v, want 403 evidence and identity", err)
	}
}

func TestCheckCreatePRFailsWithoutPushPermission(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	store.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "ghp_test"})
	server := githubValidationServer(t, http.StatusOK, false)
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials:   store,
		GitRemote:     func(context.Context, string) (string, error) { return "https://github.com/syndg/tack.git", nil },
		GitHubAPIBase: server.URL,
	})

	err = checker.Check(context.Background(), "release")
	if err == nil || !strings.Contains(err.Error(), "permissions.push=false") || !strings.Contains(err.Error(), "push permission") {
		t.Fatalf("error = %v, want no-push evidence", err)
	}
}

func TestCheckCreatePRFailsForNonGitHubRemote(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	store.SetGit(credentials.GitCredential{Type: credentials.TypePAT, Host: "github.com", Token: "ghp_test"})
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials: store,
		GitRemote:   func(context.Context, string) (string, error) { return "https://gitlab.com/syndg/tack.git", nil },
	})

	err = checker.Check(context.Background(), "release")
	if err == nil || !strings.Contains(err.Error(), "GitHub remotes only") || strings.Contains(err.Error(), "GitHub CLI fallback") {
		t.Fatalf("error = %v, want GitHub-only unsupported error without gh fallback", err)
	}
}

func TestCheckTraversesReferencedBlueprints(t *testing.T) {
	checker := New(Options{
		Blueprints: blueprints{
			"standard": {ID: "standard", Steps: []blueprint.Step{{ID: "execute", Type: blueprint.StepTypeBlueprintRef, Ref: "nested"}}},
			"nested":   {ID: "nested", Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}}},
		},
		GitRemote: func(context.Context, string) (string, error) { return "", errors.New("no origin") },
	})

	err := checker.Check(context.Background(), "standard")
	if err == nil || !strings.Contains(err.Error(), "git remote add origin") {
		t.Fatalf("error = %v, want origin fix", err)
	}
}

func TestCheckDoesNotRequireGitAuthWithoutCreatePR(t *testing.T) {
	calledGitRemote := false
	checker := New(Options{
		Blueprints: blueprints{"build": {
			ID:    "build",
			Steps: []blueprint.Step{{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder"}},
		}},
		GitRemote: func(context.Context, string) (string, error) {
			calledGitRemote = true
			return "", errors.New("should not be called")
		},
	})

	if err := checker.Check(context.Background(), "build"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if calledGitRemote {
		t.Fatal("git remote should not be checked without create_pr")
	}
}

func TestCheckRuntimeAuthRejectsCredentialTypeMismatch(t *testing.T) {
	store, err := credentials.Load(t.TempDir() + "/credentials.yaml")
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	store.SetModelProvider("openai-codex", credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: "sk-test"})
	checker := New(Options{
		Blueprints: blueprints{"build": {
			ID:    "build",
			Steps: []blueprint.Step{{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder"}},
		}},
		Credentials:         store,
		RuntimeAuthMode:     "tack",
		RuntimeAuthProvider: "openai-codex",
		RuntimeAuthMethod:   "oauth",
	})

	err = checker.Check(context.Background(), "build")
	if err == nil || !strings.Contains(err.Error(), "requires oauth credential") {
		t.Fatalf("error = %v, want credential type mismatch", err)
	}
}

func githubValidationServer(t *testing.T, repoStatus int, push bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghp_test" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		switch r.URL.Path {
		case "/user":
			_, _ = fmt.Fprint(w, `{"login":"tack-user"}`)
		case "/repos/syndg/tack":
			w.WriteHeader(repoStatus)
			if repoStatus >= 200 && repoStatus < 300 {
				_, _ = fmt.Fprintf(w, `{"full_name":"syndg/tack","permissions":{"push":%t}}`, push)
				return
			}
			_, _ = fmt.Fprint(w, `{"message":"resource not accessible by token"}`)
		default:
			t.Fatalf("unexpected GitHub path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCheckDaytonaRequiresAuthAndExternalURL(t *testing.T) {
	checker := New(Options{
		Blueprints:      blueprints{"build": {ID: "build", Steps: []blueprint.Step{{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder"}}}},
		SandboxProvider: "daytona",
	})

	err := checker.Check(context.Background(), "build")
	if err == nil {
		t.Fatal("expected daytona preflight errors")
	}
	if !strings.Contains(err.Error(), "tack auth add daytona") || !strings.Contains(err.Error(), "daemon.external_url") {
		t.Fatalf("error = %q, want daytona fixes", err.Error())
	}
}

func TestFailureExposesValidationFindings(t *testing.T) {
	failure := Failure{BlueprintID: "standard", Problems: []Problem{{
		Requirement: "git_push_permission",
		Summary:     "create_pr requires push permission to owner/repo",
		Evidence:    "permissions.push=false",
		Fix:         "use a GitHub token with push permission for this repository, or remove create_pr from the selected blueprint",
	}}}

	findings := failure.Findings()
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	finding := findings[0]
	if finding.Check != "preflight.git_push_permission" || finding.Summary != "create_pr requires push permission to owner/repo" {
		t.Fatalf("finding = %#v", finding)
	}
	if finding.Details["blueprint_id"] != "standard" || finding.Details["requirement"] != "git_push_permission" {
		t.Fatalf("details = %#v", finding.Details)
	}
	if !failure.Report().Failed {
		t.Fatal("report should fail")
	}
}
