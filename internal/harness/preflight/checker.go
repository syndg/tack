package preflight

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/harness/blueprint"
)

type BlueprintLookup interface {
	GetBlueprint(id string) (*blueprint.Blueprint, bool)
}

type Options struct {
	Blueprints          BlueprintLookup
	ProjectRoot         string
	Credentials         *credentials.Store
	RuntimeAuthMode     string
	RuntimeAuthProvider string
	SandboxProvider     string
	DaemonExternalURL   string
	GitRemote           func(context.Context, string) (string, error)
	LookPath            func(string) (string, error)
}

type Checker struct {
	blueprints          BlueprintLookup
	projectRoot         string
	credentials         *credentials.Store
	runtimeAuthMode     string
	runtimeAuthProvider string
	sandboxProvider     string
	daemonExternalURL   string
	gitRemote           func(context.Context, string) (string, error)
	lookPath            func(string) (string, error)
}

func New(opts Options) *Checker {
	c := &Checker{
		blueprints:          opts.Blueprints,
		projectRoot:         opts.ProjectRoot,
		credentials:         opts.Credentials,
		runtimeAuthMode:     opts.RuntimeAuthMode,
		runtimeAuthProvider: opts.RuntimeAuthProvider,
		sandboxProvider:     opts.SandboxProvider,
		daemonExternalURL:   opts.DaemonExternalURL,
		gitRemote:           opts.GitRemote,
		lookPath:            opts.LookPath,
	}
	if c.gitRemote == nil {
		c.gitRemote = gitOriginRemote
	}
	if c.lookPath == nil {
		c.lookPath = exec.LookPath
	}
	return c
}

func (c *Checker) Check(ctx context.Context, blueprintID string) error {
	if c == nil {
		return nil
	}
	if c.blueprints == nil {
		return errors.New("preflight failed: blueprint lookup is unavailable")
	}
	steps, err := c.stepsForBlueprint(blueprintID)
	if err != nil {
		return err
	}

	var problems []Problem
	if hasAction(steps, "create_pr") {
		problems = append(problems, c.checkPullRequestCreation(ctx)...)
	}
	if hasAgentStep(steps) && c.runtimeAuthMode == "tack" {
		problems = append(problems, c.checkRuntimeAuth()...)
	}
	if c.sandboxProvider == "daytona" {
		problems = append(problems, c.checkDaytona()...)
	}

	if len(problems) > 0 {
		return Failure{BlueprintID: blueprintID, Problems: problems}
	}
	return nil
}

func (c *Checker) stepsForBlueprint(blueprintID string) ([]blueprint.Step, error) {
	seen := map[string]bool{}
	var steps []blueprint.Step
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		bp, ok := c.blueprints.GetBlueprint(id)
		if !ok {
			return fmt.Errorf("preflight failed: blueprint %q not found", id)
		}
		for _, step := range bp.Steps {
			steps = append(steps, step)
			if step.Type == blueprint.StepTypeBlueprintRef && step.Ref != "" {
				if err := visit(step.Ref); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(blueprintID); err != nil {
		return nil, err
	}
	return steps, nil
}

func (c *Checker) checkPullRequestCreation(ctx context.Context) []Problem {
	remote, err := c.gitRemote(ctx, c.projectRoot)
	if err != nil || strings.TrimSpace(remote) == "" {
		return []Problem{{
			Requirement: "git_remote_origin",
			Summary:     "create_pr requires an origin remote",
			Fix:         "git remote add origin <url>",
		}}
	}
	_, _, host, parseErr := parseGitRemote(remote)
	if parseErr != nil {
		return []Problem{{
			Requirement: "git_remote_origin",
			Summary:     fmt.Sprintf("create_pr could not parse origin remote %q", remote),
			Fix:         "set origin to a supported GitHub SSH or HTTPS remote",
		}}
	}
	if host == "github.com" {
		if c.credentials == nil {
			return []Problem{{Requirement: "git_auth", Summary: "create_pr requires git credentials for github.com", Fix: "tack auth add git"}}
		}
		if _, err := c.credentials.GitToken(host); err != nil {
			return []Problem{{Requirement: "git_auth", Summary: "create_pr requires git credentials for github.com", Fix: "tack auth add git"}}
		}
		return nil
	}
	if _, err := c.lookPath("gh"); err != nil {
		return []Problem{{
			Requirement: "pr_provider",
			Summary:     fmt.Sprintf("create_pr for host %q requires the GitHub CLI fallback", host),
			Fix:         "install and authenticate gh, or use a github.com origin with tack auth add git",
		}}
	}
	return nil
}

func (c *Checker) checkRuntimeAuth() []Problem {
	provider := strings.TrimSpace(c.runtimeAuthProvider)
	if provider == "" {
		return []Problem{{Requirement: "runtime_auth", Summary: "agent steps require a runtime auth provider", Fix: "set runtime_auth.provider or rerun tack init"}}
	}
	if c.credentials == nil {
		return []Problem{{Requirement: "runtime_auth", Summary: fmt.Sprintf("agent steps require %s credentials", provider), Fix: "tack auth add " + provider}}
	}
	if _, err := c.credentials.ModelProvider(provider); err != nil {
		return []Problem{{Requirement: "runtime_auth", Summary: fmt.Sprintf("agent steps require %s credentials", provider), Fix: "tack auth add " + provider}}
	}
	return nil
}

func (c *Checker) checkDaytona() []Problem {
	var problems []Problem
	if strings.TrimSpace(c.daemonExternalURL) == "" {
		problems = append(problems, Problem{Requirement: "daemon_external_url", Summary: "daytona sandboxes require a daemon callback URL", Fix: "set daemon.external_url in user config"})
	}
	if c.credentials == nil {
		problems = append(problems, Problem{Requirement: "daytona_auth", Summary: "daytona sandboxes require Daytona credentials", Fix: "tack auth add daytona"})
		return problems
	}
	if _, err := c.credentials.SandboxKey("daytona"); err != nil {
		problems = append(problems, Problem{Requirement: "daytona_auth", Summary: "daytona sandboxes require Daytona credentials", Fix: "tack auth add daytona"})
	}
	return problems
}

func hasAction(steps []blueprint.Step, action string) bool {
	for _, step := range steps {
		if step.Type == blueprint.StepTypeDeterministic && step.Action == action {
			return true
		}
	}
	return false
}

func hasAgentStep(steps []blueprint.Step) bool {
	for _, step := range steps {
		if step.Type == blueprint.StepTypeAgent {
			return true
		}
	}
	return false
}

func gitOriginRemote(ctx context.Context, projectRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
