package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/runtimeauth"
)

type BlueprintLookup interface {
	GetBlueprint(id string) (*blueprint.Blueprint, bool)
}

type Options struct {
	Blueprints               BlueprintLookup
	ProjectRoot              string
	Credentials              *credentials.Store
	RuntimeAuthMode          string
	RuntimeAuthProvider      string
	RuntimeAuthMethod        string
	RuntimeAuthCredentialRef string
	SandboxProvider          string
	DaemonExternalURL        string
	GitRemote                func(context.Context, string) (string, error)
	GitHubAPIBase            string
	HTTPClient               interface {
		Do(*http.Request) (*http.Response, error)
	}
}

type Checker struct {
	blueprints               BlueprintLookup
	projectRoot              string
	credentials              *credentials.Store
	runtimeAuthMode          string
	runtimeAuthProvider      string
	runtimeAuthMethod        string
	runtimeAuthCredentialRef string
	sandboxProvider          string
	daemonExternalURL        string
	gitRemote                func(context.Context, string) (string, error)
	githubAPIBase            string
	httpClient               interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func New(opts Options) *Checker {
	c := &Checker{
		blueprints:               opts.Blueprints,
		projectRoot:              opts.ProjectRoot,
		credentials:              opts.Credentials,
		runtimeAuthMode:          opts.RuntimeAuthMode,
		runtimeAuthProvider:      opts.RuntimeAuthProvider,
		runtimeAuthMethod:        opts.RuntimeAuthMethod,
		runtimeAuthCredentialRef: opts.RuntimeAuthCredentialRef,
		sandboxProvider:          opts.SandboxProvider,
		daemonExternalURL:        opts.DaemonExternalURL,
		gitRemote:                opts.GitRemote,
		githubAPIBase:            strings.TrimRight(opts.GitHubAPIBase, "/"),
		httpClient:               opts.HTTPClient,
	}
	if c.githubAPIBase == "" {
		c.githubAPIBase = "https://api.github.com"
	}
	if c.httpClient == nil {
		c.httpClient = http.DefaultClient
	}
	if c.gitRemote == nil {
		c.gitRemote = gitOriginRemote
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
	owner, repo, host, parseErr := parseGitRemote(remote)
	if parseErr != nil {
		return []Problem{{
			Requirement: "git_remote_origin",
			Summary:     fmt.Sprintf("create_pr could not parse origin remote %q", remote),
			Evidence:    parseErr.Error(),
			Fix:         "set origin to a supported GitHub SSH or HTTPS remote",
		}}
	}
	if host != "github.com" {
		return []Problem{{
			Requirement: "pr_provider",
			Summary:     fmt.Sprintf("create_pr supports GitHub remotes only; origin host %q is unsupported", host),
			Evidence:    "origin=" + remote,
			Fix:         "set origin to a GitHub repository or remove create_pr from the selected blueprint",
		}}
	}
	if c.credentials == nil {
		return []Problem{{
			Requirement: "git_auth",
			Summary:     "create_pr requires stored git credentials for github.com",
			Evidence:    "origin=" + remote,
			Fix:         "tack auth add github",
		}}
	}
	token, err := c.credentials.GitToken(host)
	if err != nil {
		return []Problem{{
			Requirement: "git_auth",
			Summary:     "create_pr requires stored git credentials for github.com",
			Evidence:    fmt.Sprintf("origin=%s; %v", remote, err),
			Fix:         "tack auth add github",
		}}
	}
	return c.checkGitHubPushPermission(ctx, remote, owner, repo, token)
}

type githubUserResponse struct {
	Login   string `json:"login"`
	Message string `json:"message"`
}

type githubRepoResponse struct {
	FullName    string `json:"full_name"`
	Message     string `json:"message"`
	Permissions struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (c *Checker) checkGitHubPushPermission(ctx context.Context, remote, owner, repo, token string) []Problem {
	login, problem := c.githubAuthenticatedLogin(ctx, token)
	if problem != nil {
		problem.Evidence = joinEvidence("origin="+remote, problem.Evidence)
		return []Problem{*problem}
	}

	repoInfo, status, body, err := c.githubRepo(ctx, owner, repo, token)
	identity := "authenticated_identity=" + login
	if err != nil {
		return []Problem{{
			Requirement: "git_push_permission",
			Summary:     fmt.Sprintf("create_pr could not validate GitHub repository access for %s/%s", owner, repo),
			Evidence:    joinEvidence("origin="+remote, identity, err.Error()),
			Fix:         "verify the GitHub token can access the repository with push permission",
		}}
	}
	if status < 200 || status >= 300 {
		msg := strings.TrimSpace(repoInfo.Message)
		if msg == "" {
			msg = strings.TrimSpace(body)
		}
		return []Problem{{
			Requirement: "git_push_permission",
			Summary:     fmt.Sprintf("create_pr requires GitHub repository access for %s/%s", owner, repo),
			Evidence:    joinEvidence("origin="+remote, identity, fmt.Sprintf("github /repos status=%d message=%s", status, msg)),
			Fix:         "grant the stored GitHub token repository access with push permission, or remove create_pr from the selected blueprint",
		}}
	}
	if !repoInfo.Permissions.Push {
		fullName := strings.TrimSpace(repoInfo.FullName)
		if fullName == "" {
			fullName = owner + "/" + repo
		}
		return []Problem{{
			Requirement: "git_push_permission",
			Summary:     fmt.Sprintf("create_pr requires push permission to %s", fullName),
			Evidence:    joinEvidence("origin="+remote, identity, "permissions.push=false"),
			Fix:         "use a GitHub token with push permission for this repository, or remove create_pr from the selected blueprint",
		}}
	}
	return nil
}

func (c *Checker) githubAuthenticatedLogin(ctx context.Context, token string) (string, *Problem) {
	var user githubUserResponse
	status, body, err := c.githubGet(ctx, "/user", token, &user)
	if err != nil {
		return "", &Problem{Requirement: "git_auth", Summary: "create_pr could not validate the stored GitHub token", Evidence: err.Error(), Fix: "tack auth add github"}
	}
	if status < 200 || status >= 300 {
		msg := strings.TrimSpace(user.Message)
		if msg == "" {
			msg = strings.TrimSpace(body)
		}
		return "", &Problem{Requirement: "git_auth", Summary: "create_pr requires a valid GitHub token", Evidence: fmt.Sprintf("github /user status=%d message=%s", status, msg), Fix: "tack auth add github"}
	}
	if strings.TrimSpace(user.Login) == "" {
		return "", &Problem{Requirement: "git_auth", Summary: "create_pr could not determine the GitHub token identity", Evidence: "github /user returned empty login", Fix: "tack auth add github"}
	}
	return strings.TrimSpace(user.Login), nil
}

func (c *Checker) githubRepo(ctx context.Context, owner, repo, token string) (githubRepoResponse, int, string, error) {
	var repoInfo githubRepoResponse
	status, body, err := c.githubGet(ctx, "/repos/"+owner+"/"+repo, token, &repoInfo)
	return repoInfo, status, body, err
}

func (c *Checker) githubGet(ctx context.Context, path, token string, target any) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.githubAPIBase+path, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)
	if len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, target)
	}
	return resp.StatusCode, body, nil
}

func joinEvidence(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, strings.TrimSpace(part))
		}
	}
	return strings.Join(kept, "; ")
}

func (c *Checker) checkRuntimeAuth() []Problem {
	provider := strings.TrimSpace(c.runtimeAuthProvider)
	if provider == "" {
		return []Problem{{Requirement: "runtime_auth", Summary: "agent steps require a runtime auth provider", Fix: "set runtime_auth.provider or rerun tack init"}}
	}
	if c.credentials == nil {
		return []Problem{{Requirement: "runtime_auth", Summary: fmt.Sprintf("agent steps require %s credentials", provider), Fix: "tack auth add " + provider}}
	}
	_, err := runtimeauth.ResolveCredentialBinding(config.RuntimeAuthConfig{
		Mode:          c.runtimeAuthMode,
		Provider:      provider,
		Method:        c.runtimeAuthMethod,
		CredentialRef: c.runtimeAuthCredentialRef,
	}, c.credentials)
	if err != nil {
		return []Problem{{Requirement: "runtime_auth", Summary: fmt.Sprintf("agent steps require usable %s credentials: %v", provider, err), Fix: "tack auth add " + provider}}
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
