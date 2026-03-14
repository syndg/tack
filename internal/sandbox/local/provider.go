package local

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/sandbox"
)

// Provider creates sandboxes as local git worktrees.
// Each sandbox is an isolated worktree with its own branch.
type Provider struct {
	repoRoot    string   // path to the main git repository
	worktreeDir string   // base directory for worktrees
	postCreate  []string // commands to run after worktree creation
	mu          sync.Mutex
	sandboxes   map[string]*LocalSandbox
	logger      *slog.Logger
}

func New(repoRoot string, worktreeDir string, logger *slog.Logger) *Provider {
	return &Provider{
		repoRoot:    repoRoot,
		worktreeDir: worktreeDir,
		sandboxes:   make(map[string]*LocalSandbox),
		logger:      logger,
	}
}

// SetPostCreate sets commands to run after worktree creation (e.g., "bun install").
func (p *Provider) SetPostCreate(commands []string) {
	p.postCreate = commands
}

// Create provisions a new git worktree sandbox.
func (p *Provider) Create(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
	id := uuid.New().String()

	// Build branch name: deck/{objective[:8]}/{role}-{id[:8]}
	objective := opts.Labels["deck.objective"]
	if len(objective) > 8 {
		objective = objective[:8]
	}
	role := opts.Labels["deck.role"]
	if role == "" {
		role = "agent"
	}
	idShort := id[:8]
	branch := fmt.Sprintf("deck/%s/%s-%s", objective, role, idShort)

	worktreePath := filepath.Join(p.worktreeDir, id)

	p.logger.Info("creating sandbox worktree",
		"id", id,
		"branch", branch,
		"path", worktreePath,
	)

	// git worktree add {path} -b {branch}
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", worktreePath, "-b", branch)
	cmd.Dir = p.repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("creating git worktree: %w (stderr: %s)", err, stderr.String())
	}

	// Exclude Deck runtime artifacts from git so auto-commit doesn't include them.
	// Use the repo's shared info/exclude which is outside the working tree and
	// won't show up as a change in any worktree.
	infoExclude := filepath.Join(p.repoRoot, ".git", "info", "exclude")
	if data, err := os.ReadFile(infoExclude); err == nil {
		if !strings.Contains(string(data), ".deck-ext") {
			_ = os.WriteFile(infoExclude, append(data, []byte("\n.deck-ext\n")...), 0o644)
		}
	}

	// Copy gitignored files (node_modules, build caches, .env) from main repo.
	if err := copyIgnoredFiles(p.repoRoot, worktreePath, p.logger); err != nil {
		p.logger.Warn("copy-ignored failed, continuing", "error", err)
	}

	// Run post-create commands (e.g., "bun install").
	for _, cmd := range p.postCreate {
		p.logger.Info("running post-create command", "command", cmd, "worktree", id)
		c := exec.CommandContext(ctx, "sh", "-c", cmd)
		c.Dir = worktreePath
		c.Env = os.Environ()
		if out, err := c.CombinedOutput(); err != nil {
			p.logger.Warn("post-create command failed", "command", cmd, "error", err, "output", string(out))
		}
	}

	sb := &LocalSandbox{
		id:      id,
		path:    worktreePath,
		branch:  branch,
		labels:  opts.Labels,
		status:  sandbox.SandboxStatusRunning,
		envVars: opts.EnvVars,
	}

	p.mu.Lock()
	p.sandboxes[id] = sb
	p.mu.Unlock()

	return sb, nil
}

// Get retrieves a sandbox by ID from the internal map.
func (p *Provider) Get(ctx context.Context, id string) (sandbox.Sandbox, error) {
	p.mu.Lock()
	sb, ok := p.sandboxes[id]
	p.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("sandbox %s not found", id)
	}
	return sb, nil
}

// List returns sandboxes matching the given label filters (AND logic).
func (p *Provider) List(ctx context.Context, labels map[string]string) ([]sandbox.Sandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var result []sandbox.Sandbox
	for _, sb := range p.sandboxes {
		if matchesLabels(sb.labels, labels) {
			result = append(result, sb)
		}
	}
	return result, nil
}

// Delete removes the worktree and its branch.
func (p *Provider) Delete(ctx context.Context, id string) error {
	p.mu.Lock()
	sb, ok := p.sandboxes[id]
	if ok {
		delete(p.sandboxes, id)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("sandbox %s not found", id)
	}

	p.logger.Info("deleting sandbox worktree", "id", id, "path", sb.path, "branch", sb.branch)

	// git worktree remove {path} --force
	var rmErr error
	{
		cmd := exec.CommandContext(ctx, "git", "worktree", "remove", sb.path, "--force")
		cmd.Dir = p.repoRoot
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			rmErr = fmt.Errorf("removing git worktree: %w (stderr: %s)", err, stderr.String())
		}
	}

	// git branch -D {branch}
	var branchErr error
	{
		cmd := exec.CommandContext(ctx, "git", "branch", "-D", sb.branch)
		cmd.Dir = p.repoRoot
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			branchErr = fmt.Errorf("deleting git branch: %w (stderr: %s)", err, stderr.String())
		}
	}

	if rmErr != nil {
		return rmErr
	}
	return branchErr
}

// matchesLabels returns true if all filter labels are present and equal in target.
func matchesLabels(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
			return false
		}
	}
	return true
}

// LocalSandbox implements sandbox.Sandbox backed by a git worktree.
type LocalSandbox struct {
	id      string
	path    string // worktree absolute path
	branch  string // git branch name
	labels  map[string]string
	status  sandbox.SandboxStatus
	envVars map[string]string
	mu      sync.Mutex
}

func (s *LocalSandbox) ID() string {
	return s.id
}

func (s *LocalSandbox) Status() sandbox.SandboxStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Exec runs a shell command in the worktree directory.
func (s *LocalSandbox) Exec(ctx context.Context, cmdStr string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	// Set working directory
	workDir := s.path
	if opts.WorkDir != "" {
		workDir = filepath.Join(s.path, opts.WorkDir)
	}
	cmd.Dir = workDir

	// Merge env: inherit OS env, then sandbox envVars, then opts.Env
	env := os.Environ()
	for k, v := range s.envVars {
		env = append(env, k+"="+v)
	}
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return sandbox.ExecResult{}, fmt.Errorf("executing command: %w", err)
		}
	}

	return sandbox.ExecResult{
		ExitCode: exitCode,
		Stdout:   strings.TrimRight(stdout.String(), "\n"),
		Stderr:   strings.TrimRight(stderr.String(), "\n"),
	}, nil
}

// ExecStreaming starts a long-running process and returns a ProcessHandle for
// bidirectional stdin/stdout communication.
func (s *LocalSandbox) ExecStreaming(ctx context.Context, cmdStr string, opts sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	workDir := s.path
	if opts.WorkDir != "" {
		workDir = filepath.Join(s.path, opts.WorkDir)
	}
	cmd.Dir = workDir

	env := os.Environ()
	for k, v := range s.envVars {
		env = append(env, k+"="+v)
	}
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting process: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// Pi can emit very large JSONL lines (e.g. file contents in tool results).
	// Default 64KB is too small; use 4MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	return &localProcessHandle{
		cmd:     cmd,
		stdin:   stdin,
		scanner: scanner,
	}, nil
}

// localProcessHandle wraps exec.Command pipes to implement sandbox.ProcessHandle.
type localProcessHandle struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
}

func (h *localProcessHandle) Write(data []byte) error {
	_, err := h.stdin.Write(data)
	return err
}

func (h *localProcessHandle) ReadLine() (string, error) {
	if h.scanner.Scan() {
		return h.scanner.Text(), nil
	}
	if err := h.scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

func (h *localProcessHandle) Wait() (int, error) {
	err := h.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func (h *localProcessHandle) Kill() error {
	if h.cmd.Process != nil {
		return h.cmd.Process.Kill()
	}
	return nil
}

// Upload writes content to a file within the worktree.
func (s *LocalSandbox) Upload(ctx context.Context, content []byte, path string) error {
	dest := filepath.Join(s.path, path)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating parent directories: %w", err)
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}

// Download reads a file from the worktree.
func (s *LocalSandbox) Download(ctx context.Context, path string) ([]byte, error) {
	src := filepath.Join(s.path, path)
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return data, nil
}

// Stop sets status to "stopped". No-op for local worktrees (they persist until Delete).
func (s *LocalSandbox) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = sandbox.SandboxStatusStopped
	return nil
}

// Start sets status to "running". Only valid if currently "stopped".
func (s *LocalSandbox) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != sandbox.SandboxStatusStopped {
		return fmt.Errorf("sandbox is not stopped (current status: %s)", s.status)
	}
	s.status = sandbox.SandboxStatusRunning
	return nil
}

// Health returns an error if the worktree directory no longer exists.
func (s *LocalSandbox) Health() error {
	if _, err := os.Stat(s.path); err != nil {
		return fmt.Errorf("worktree directory unavailable: %w", err)
	}
	return nil
}
