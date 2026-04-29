package preflight

import (
	"context"
	"errors"
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
	checker := New(Options{
		Blueprints: blueprints{"release": {
			ID:    "release",
			Steps: []blueprint.Step{{ID: "pr", Type: blueprint.StepTypeDeterministic, Action: "create_pr"}},
		}},
		Credentials: store,
		GitRemote:   func(context.Context, string) (string, error) { return "https://github.com/syndg/tack.git", nil },
	})

	if err := checker.Check(context.Background(), "release"); err != nil {
		t.Fatalf("Check: %v", err)
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
	checker := New(Options{
		Blueprints: blueprints{"build": {
			ID:    "build",
			Steps: []blueprint.Step{{ID: "build", Type: blueprint.StepTypeAgent, Role: "builder"}},
		}},
	})

	if err := checker.Check(context.Background(), "build"); err != nil {
		t.Fatalf("Check: %v", err)
	}
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
