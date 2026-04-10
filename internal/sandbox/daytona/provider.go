package daytona

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	pathpkg "path"
	"regexp"
	"strings"
	"sync"
	"time"

	daytona "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
	"github.com/syndg/tack/internal/naming"

	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/sandbox"
)

// ptyExecSetupDelay is the wait time after sending exec to a PTY before
// reading output. The PTY needs a moment for exec to replace the shell
// process; without this delay, the first read may capture shell artifacts
// (prompt, echo) instead of the command's stdout.
const ptyExecSetupDelay = 200 * time.Millisecond

// Provider creates sandboxes via the Daytona SDK.
type Provider struct {
	client    *daytona.Client
	cfg       Config
	creds     *credentials.Store
	mu        sync.Mutex
	sandboxes map[string]*DaytonaSandbox
	logger    *slog.Logger
}

// Config holds Daytona connection settings.
type Config struct {
	APIKey       string
	APIURL       string
	Snapshot     string
	RepoURL      string // git remote URL for clone/pull
	RepoPath     string // path inside sandbox where repo lives (default: /home/daytona/project)
	ProjectSetup sandbox.ProjectSetup
}

func New(cfg Config, creds *credentials.Store, logger *slog.Logger) (*Provider, error) {
	var client *daytona.Client
	var err error

	if cfg.APIURL != "" || cfg.APIKey != "" {
		client, err = daytona.NewClientWithConfig(&types.DaytonaConfig{
			APIKey: cfg.APIKey,
			APIUrl: cfg.APIURL,
		})
	} else {
		client, err = daytona.NewClient()
	}
	if err != nil {
		return nil, fmt.Errorf("creating daytona client: %w", err)
	}

	if cfg.RepoPath == "" {
		cfg.RepoPath = "/home/daytona/project"
	}

	return &Provider{
		client:    client,
		cfg:       cfg,
		creds:     creds,
		sandboxes: make(map[string]*DaytonaSandbox),
		logger:    logger,
	}, nil
}

func (p *Provider) Create(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
	p.logger.Info("creating daytona sandbox", "name", opts.Name)

	snapshot := p.cfg.Snapshot
	if opts.Snapshot != "" {
		snapshot = opts.Snapshot
	}

	params := types.SnapshotParams{
		Snapshot: snapshot,
		SandboxBaseParams: types.SandboxBaseParams{
			Name:      opts.Name,
			Labels:    opts.Labels,
			EnvVars:   opts.EnvVars,
			Ephemeral: opts.Ephemeral,
		},
	}

	dSandbox, err := p.client.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating daytona sandbox: %w", err)
	}

	// Bootstrap phase: set up repo + branch + post-create.
	if p.cfg.RepoURL != "" {
		if err := p.bootstrap(ctx, dSandbox, opts); err != nil {
			p.logger.Error("bootstrap failed, deleting sandbox", "id", dSandbox.ID, "error", err)
			_ = dSandbox.Delete(ctx)
			return nil, fmt.Errorf("bootstrapping sandbox: %w", err)
		}
	}

	sb := &DaytonaSandbox{
		sandbox:       dSandbox,
		labels:        opts.Labels,
		envVars:       opts.EnvVars,
		workDir:       p.cfg.RepoPath,
		gitAuthHeader: p.gitAuthHeader(),
		logger:        p.logger,
	}

	p.mu.Lock()
	p.sandboxes[dSandbox.ID] = sb
	p.mu.Unlock()

	return sb, nil
}

// sshToHTTPS converts git@github.com:user/repo.git to https://github.com/user/repo.git.
// Returns the original URL if it's not an SSH URL.
func sshToHTTPS(url string) string {
	if !strings.HasPrefix(url, "git@") {
		return url
	}
	// git@github.com:user/repo.git → https://github.com/user/repo.git
	url = strings.TrimPrefix(url, "git@")
	url = strings.Replace(url, ":", "/", 1)
	return "https://" + url
}

func (p *Provider) gitAuthHeader() string {
	if p.creds == nil {
		return ""
	}
	tok, err := p.creds.GitToken("")
	if err != nil || tok == "" {
		return ""
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	return "Authorization: Basic " + basic
}

func resolveRemotePath(base, requested string) (string, error) {
	if base == "" {
		base = "/"
	}
	cleanBase := pathpkg.Clean(base)
	if requested == "" {
		return cleanBase, nil
	}
	var candidate string
	if pathpkg.IsAbs(requested) {
		candidate = pathpkg.Clean(requested)
	} else {
		candidate = pathpkg.Clean(pathpkg.Join(cleanBase, requested))
	}
	if candidate != cleanBase && !strings.HasPrefix(candidate, cleanBase+"/") {
		return "", fmt.Errorf("path escapes sandbox root: %s", requested)
	}
	return candidate, nil
}

// bootstrap sets up the repo and working branch inside a freshly created sandbox.
func (p *Provider) bootstrap(ctx context.Context, sb *daytona.Sandbox, opts sandbox.CreateOpts) error {
	repoPath := p.cfg.RepoPath

	// Daytona Git API only supports HTTPS auth — convert SSH URLs.
	repoURL := sshToHTTPS(p.cfg.RepoURL)

	// Resolve git credentials for clone/pull auth.
	var gitOpts []func(*options.GitClone)
	var pullOpts []func(*options.GitPull)
	if p.creds != nil {
		tok, err := p.creds.GitToken("")
		if err == nil && tok != "" {
			gitOpts = append(gitOpts, options.WithUsername("x-access-token"), options.WithPassword(tok))
			pullOpts = append(pullOpts, options.WithPullUsername("x-access-token"), options.WithPullPassword(tok))
		}
	}

	if p.cfg.Snapshot != "" {
		// Snapshot model: repo is already present, just pull latest.
		p.logger.Info("bootstrap: pulling latest into snapshot", "sandbox", sb.ID)
		if err := sb.Git.Pull(ctx, repoPath, pullOpts...); err != nil {
			p.logger.Warn("bootstrap: git pull failed (may be clean)", "error", err)
			// Non-fatal — snapshot might already be at HEAD.
		}
	} else {
		// No snapshot: clone from scratch.
		p.logger.Info("bootstrap: cloning repo", "sandbox", sb.ID, "url", repoURL)
		if err := sb.Git.Clone(ctx, repoURL, repoPath, gitOpts...); err != nil {
			return fmt.Errorf("cloning repo: %w", err)
		}
	}

	// Create and checkout the tack working branch.
	branch := opts.Branch
	if branch == "" {
		branch = sandbox.DeriveBranchPrefix(opts)
	}

	if opts.BaseRef != "" {
		p.logger.Info("bootstrap: creating branch from base ref", "branch", branch, "base_ref", opts.BaseRef)
		// Ensure we have remote refs available when the base ref is a pushed merge branch.
		_, _ = sb.Process.ExecuteCommand(ctx, "git fetch origin '+refs/heads/*:refs/remotes/origin/*'", options.WithCwd(repoPath))
		resp, err := sb.Process.ExecuteCommand(ctx, fmt.Sprintf("git checkout -B %s origin/%s", branch, opts.BaseRef), options.WithCwd(repoPath))
		if err != nil || resp.ExitCode != 0 {
			resp, err = sb.Process.ExecuteCommand(ctx, fmt.Sprintf("git checkout -B %s %s", branch, opts.BaseRef), options.WithCwd(repoPath))
			if err != nil || resp.ExitCode != 0 {
				return fmt.Errorf("checking out branch %s from %s: %s", branch, opts.BaseRef, resp.Result)
			}
		}
	} else {
		p.logger.Info("bootstrap: creating branch", "branch", branch)
		if err := sb.Git.CreateBranch(ctx, repoPath, branch); err != nil {
			p.logger.Warn("bootstrap: create branch failed (may exist)", "error", err)
		}
		if err := sb.Git.Checkout(ctx, repoPath, branch); err != nil {
			return fmt.Errorf("checking out branch %s: %w", branch, err)
		}
	}

	if err := sandbox.RunProjectSetup(ctx, p.cfg.ProjectSetup, func(ctx context.Context, command string) (sandbox.ExecResult, error) {
		resp, err := sb.Process.ExecuteCommand(ctx, command, options.WithCwd(repoPath))
		if err != nil {
			return sandbox.ExecResult{}, err
		}
		return sandbox.ExecResult{ExitCode: resp.ExitCode, Stdout: strings.TrimSpace(resp.Result)}, nil
	}, p.logger); err != nil {
		return fmt.Errorf("running project setup: %w", err)
	}

	return nil
}

func (p *Provider) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	p.mu.Lock()
	sb, ok := p.sandboxes[id]
	p.mu.Unlock()
	if ok {
		return sb, nil
	}

	dSandbox, err := p.client.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("sandbox %s not found: %w", id, err)
	}

	sb = &DaytonaSandbox{
		sandbox:       dSandbox,
		workDir:       p.cfg.RepoPath,
		gitAuthHeader: p.gitAuthHeader(),
		logger:        p.logger,
	}

	p.mu.Lock()
	p.sandboxes[id] = sb
	p.mu.Unlock()

	return sb, nil
}

func (p *Provider) List(ctx context.Context, labels map[string]string) ([]sandbox.Sandbox, error) {
	// Query the Daytona API so sandboxes survive daemon restarts.
	if p.client == nil {
		return p.listFromCache(labels), nil
	}
	paginated, err := p.client.List(ctx, labels, nil, nil)
	if err != nil {
		// Fall back to in-memory cache on API failure.
		p.logger.Warn("daytona API list failed, using cache", "error", err)
		return p.listFromCache(labels), nil
	}

	result := make([]sandbox.Sandbox, 0, len(paginated.Items))
	p.mu.Lock()
	for _, dSandbox := range paginated.Items {
		// Prefer cached entry (has labels and envVars); otherwise wrap the API result.
		if cached, ok := p.sandboxes[dSandbox.ID]; ok {
			result = append(result, cached)
		} else {
			sb := &DaytonaSandbox{
				sandbox:       dSandbox,
				labels:        labels, // API already filtered by these labels.
				gitAuthHeader: p.gitAuthHeader(),
				logger:        p.logger,
			}
			p.sandboxes[dSandbox.ID] = sb
			result = append(result, sb)
		}
	}
	p.mu.Unlock()

	return result, nil
}

func (p *Provider) Delete(ctx context.Context, id string) error {
	p.mu.Lock()
	sb, ok := p.sandboxes[id]
	if ok {
		delete(p.sandboxes, id)
	}
	p.mu.Unlock()

	if ok {
		p.logger.Info("deleting daytona sandbox", "id", id)
		return sb.sandbox.Delete(ctx)
	}

	// Not in cache — try via API (e.g., after daemon restart).
	if p.client == nil {
		return fmt.Errorf("sandbox %s not found", id)
	}
	dSandbox, err := p.client.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("sandbox %s not found: %w", id, err)
	}
	p.logger.Info("deleting daytona sandbox (rediscovered)", "id", id)
	return dSandbox.Delete(ctx)
}

func (p *Provider) listFromCache(labels map[string]string) []sandbox.Sandbox {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result []sandbox.Sandbox
	for _, sb := range p.sandboxes {
		if matchesLabels(sb.labels, labels) {
			result = append(result, sb)
		}
	}
	return result
}

// matchesLabels delegates to the shared sandbox.MatchesLabels helper.
var matchesLabels = sandbox.MatchesLabels

// buildEffectiveCommand wraps a command with env exports when the Daytona
// toolbox API doesn't support per-command env vars natively. The entire
// command is wrapped in sh -c so exports take effect before the command runs.
func buildEffectiveCommand(cmd string, opts sandbox.ExecOpts) string {
	if len(opts.Env) == 0 {
		return cmd
	}
	var exports []string
	for k, v := range opts.Env {
		exports = append(exports, fmt.Sprintf("export %s=%s", k, naming.ShellQuote(v)))
	}
	inner := strings.Join(exports, " && ") + " && " + cmd
	return "sh -c " + naming.ShellQuote(inner)
}

// DaytonaSandbox wraps a Daytona SDK sandbox to implement sandbox.Sandbox.
type DaytonaSandbox struct {
	sandbox       *daytona.Sandbox
	labels        map[string]string
	envVars       map[string]string
	workDir       string // default working directory (repo path after bootstrap)
	gitAuthHeader string
	logger        *slog.Logger
}

func (s *DaytonaSandbox) ID() string {
	return s.sandbox.ID
}

func (s *DaytonaSandbox) Status() sandbox.SandboxStatus {
	return sandbox.SandboxStatusRunning
}

func (s *DaytonaSandbox) Exec(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	mergedEnv := make(map[string]string, len(s.envVars)+len(opts.Env)+3)
	for k, v := range s.envVars {
		mergedEnv[k] = v
	}
	for k, v := range opts.Env {
		mergedEnv[k] = v
	}
	if s.gitAuthHeader != "" && strings.HasPrefix(strings.TrimSpace(cmd), "git ") {
		mergedEnv["GIT_CONFIG_COUNT"] = "1"
		mergedEnv["GIT_CONFIG_KEY_0"] = "http.extraHeader"
		mergedEnv["GIT_CONFIG_VALUE_0"] = s.gitAuthHeader
	}
	// Build the effective command with env and workdir from ExecOpts.
	effectiveCmd := buildEffectiveCommand(cmd, sandbox.ExecOpts{Env: mergedEnv})

	var execOpts []func(*options.ExecuteCommand)
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = s.workDir
	} else {
		resolved, err := resolveRemotePath(s.workDir, workDir)
		if err != nil {
			return sandbox.ExecResult{}, err
		}
		workDir = resolved
	}
	if workDir != "" {
		execOpts = append(execOpts, options.WithCwd(workDir))
	}
	if opts.Timeout > 0 {
		execOpts = append(execOpts, options.WithExecuteTimeout(opts.Timeout))
	}

	resp, err := s.sandbox.Process.ExecuteCommand(ctx, effectiveCmd, execOpts...)
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("executing command: %w", err)
	}

	return sandbox.ExecResult{
		ExitCode: resp.ExitCode,
		Stdout:   strings.TrimRight(resp.Result, "\n"),
	}, nil
}

func (s *DaytonaSandbox) ExecStreaming(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	// Use a unique session ID (not the command itself, which can be very long).
	sessionID := fmt.Sprintf("tack-%d", time.Now().UnixNano())

	var ptyOpts []func(*options.CreatePty)
	if len(opts.Env) > 0 || len(s.envVars) > 0 {
		merged := make(map[string]string, len(s.envVars)+len(opts.Env))
		for k, v := range s.envVars {
			merged[k] = v
		}
		for k, v := range opts.Env {
			merged[k] = v
		}
		ptyOpts = append(ptyOpts, options.WithCreatePtyEnv(merged))
	}

	pty, err := s.sandbox.Process.CreatePty(ctx, sessionID, ptyOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating PTY: %w", err)
	}

	// Resolve working directory (fallback to repo dir).
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = s.workDir
	} else {
		resolved, err := resolveRemotePath(s.workDir, workDir)
		if err != nil {
			return nil, err
		}
		workDir = resolved
	}

	// Wait for the PTY WebSocket connection to be established before sending input.
	if err := pty.WaitForConnection(ctx); err != nil {
		pty.Disconnect()
		return nil, fmt.Errorf("waiting for PTY connection: %w", err)
	}

	// Suppress shell artifacts before launching the command:
	// - PS1="" kills the prompt
	// - stty -echo disables terminal echo (so our JSON input isn't echoed back)
	// - exec replaces the shell with the command (clean stdin/stdout, no shell interference)
	// - cd MUST happen before exec (cd is a builtin, exec replaces the shell)
	// This is critical for RPC-based agents (Pi) that use JSON over stdin/stdout.
	// Build the PTY command:
	// 1. Disable prompt and echo to keep stdout clean for JSON RPC
	// 2. cd to the working directory
	// 3. exec replaces the shell with the target command (clean stdin/stdout)
	var parts []string
	parts = append(parts, `export PS1=""`, `stty -echo 2>/dev/null`)
	if workDir != "" {
		parts = append(parts, fmt.Sprintf("cd %s", naming.ShellQuote(workDir)))
	}
	parts = append(parts, "exec "+cmd)
	shellSetup := strings.Join(parts, " && ")
	if err := pty.SendInput([]byte(shellSetup + "\n")); err != nil {
		pty.Disconnect()
		return nil, fmt.Errorf("sending command to PTY: %w", err)
	}

	// Give exec a moment to replace the shell before we start reading.
	time.Sleep(ptyExecSetupDelay)

	scanner := bufio.NewScanner(pty)
	// Pi can emit very large JSONL lines (file contents in tool results,
	// agent_end with full conversation history). 16MB handles planners.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	return &daytonaPtyHandle{
		pty:     pty,
		scanner: scanner,
	}, nil
}

func (s *DaytonaSandbox) Upload(ctx context.Context, content []byte, path string) error {
	resolved, err := resolveRemotePath(s.workDir, path)
	if err != nil {
		return err
	}
	return s.sandbox.FileSystem.UploadFile(ctx, content, resolved)
}

func (s *DaytonaSandbox) Download(ctx context.Context, path string) ([]byte, error) {
	resolved, err := resolveRemotePath(s.workDir, path)
	if err != nil {
		return nil, err
	}
	return s.sandbox.FileSystem.DownloadFile(ctx, resolved, nil)
}

func (s *DaytonaSandbox) Stop(ctx context.Context) error {
	return s.sandbox.Stop(ctx)
}

func (s *DaytonaSandbox) Start(ctx context.Context) error {
	return s.sandbox.Start(ctx)
}

func (s *DaytonaSandbox) Health() error {
	return nil
}

// daytonaPtyHandle implements sandbox.ProcessHandle over a Daytona PTY.
type daytonaPtyHandle struct {
	pty     *daytona.PtyHandle
	scanner *bufio.Scanner
}

// ansiEscape matches ANSI escape sequences for stripping from PTY output.
var ansiEscape = regexp.MustCompile(`\x1b[\[\(][0-9;?]*[a-zA-Z=>lh]`)

// controlChars matches other terminal control characters that aren't ANSI escapes.
var controlChars = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1a\x7f]`)

func (h *daytonaPtyHandle) Write(data []byte) error {
	return h.pty.SendInput(data)
}

func (h *daytonaPtyHandle) ReadLine() (string, error) {
	for {
		if !h.scanner.Scan() {
			if err := h.scanner.Err(); err != nil {
				return "", err
			}
			return "", io.EOF
		}
		line := h.scanner.Text()
		line = strings.ReplaceAll(line, "\r", "")
		line = strings.TrimSpace(line)
		// Skip empty lines (common after stripping terminal artifacts).
		if line == "" {
			continue
		}
		// Only strip ANSI/control chars from the prefix before the JSON
		// object starts. Stripping within JSON content corrupts escaped
		// strings (e.g. agent_end payloads with full conversation history).
		if idx := strings.Index(line, "{"); idx > 0 {
			prefix := line[:idx]
			prefix = ansiEscape.ReplaceAllString(prefix, "")
			prefix = controlChars.ReplaceAllString(prefix, "")
			line = strings.TrimSpace(prefix) + line[idx:]
		} else if !strings.HasPrefix(line, "{") {
			// Non-JSON line — strip fully.
			line = ansiEscape.ReplaceAllString(line, "")
			line = controlChars.ReplaceAllString(line, "")
			line = strings.TrimSpace(line)
		}
		// Strip trailing ANSI/control chars after the last '}' so strict
		// JSON parsers don't choke on e.g. trailing \x1b[0m.
		if lastBrace := strings.LastIndex(line, "}"); lastBrace >= 0 && lastBrace < len(line)-1 {
			suffix := line[lastBrace+1:]
			suffix = ansiEscape.ReplaceAllString(suffix, "")
			suffix = controlChars.ReplaceAllString(suffix, "")
			suffix = strings.TrimSpace(suffix)
			line = line[:lastBrace+1] + suffix
		}
		if line == "" {
			continue
		}
		return line, nil
	}
}

func (h *daytonaPtyHandle) Wait() (int, error) {
	ctx := context.Background()
	result, err := h.pty.Wait(ctx)
	if err != nil {
		return -1, err
	}
	if result.ExitCode != nil {
		return *result.ExitCode, nil
	}
	return 0, nil
}

func (h *daytonaPtyHandle) Kill() error {
	return h.pty.Disconnect()
}
