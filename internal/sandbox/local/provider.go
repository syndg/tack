package local

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/sandbox"
)

func resolveWithin(base, requested string) (string, error) {
	baseResolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("resolving sandbox root: %w", err)
	}
	if requested == "" {
		return baseResolved, nil
	}
	candidate := filepath.Clean(filepath.Join(baseResolved, requested))
	resolved := candidate
	for probe := candidate; ; probe = filepath.Dir(probe) {
		resolvedProbe, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if probe != candidate {
				suffix, relErr := filepath.Rel(probe, candidate)
				if relErr != nil {
					return "", fmt.Errorf("resolving path suffix: %w", relErr)
				}
				resolved = filepath.Join(resolvedProbe, suffix)
			} else {
				resolved = resolvedProbe
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolving path: %w", err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("path escapes sandbox root: %s", requested)
		}
	}
	rel, err := filepath.Rel(baseResolved, resolved)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox root: %s", requested)
	}
	return resolved, nil
}

// validBranchRe matches branch names that start with an alphanumeric char or
// "tack/" and contain only alphanumeric, hyphen, underscore, dot, and slash.
var validBranchRe = regexp.MustCompile(`^[a-zA-Z0-9][-a-zA-Z0-9_.\/]*$`)

const sandboxMetaFile = ".tack-sandbox.json"

// Provider creates sandboxes as local git worktrees.
// Each sandbox is an isolated worktree with its own branch.
type Provider struct {
	repoRoot     string // path to the main git repository
	worktreeDir  string // base directory for worktrees
	projectSetup sandbox.ProjectSetup
	mu           sync.Mutex
	sandboxes    map[string]*LocalSandbox
	logger       *slog.Logger
}

func New(repoRoot string, worktreeDir string, logger *slog.Logger) *Provider {
	return &Provider{
		repoRoot:    repoRoot,
		worktreeDir: worktreeDir,
		sandboxes:   make(map[string]*LocalSandbox),
		logger:      logger,
	}
}

// Rediscover scans existing git worktrees and repopulates the in-memory
// sandbox map. Labels are read from a .tack-sandbox.json metadata file
// persisted inside each worktree at creation time, so full (un-truncated)
// label values survive daemon restarts.
func (p *Provider) Rediscover(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = p.repoRoot
	out, err := cmd.Output()
	if err != nil {
		p.logger.Error("rediscover: git worktree list failed", "error", err)
		return
	}

	// Resolve symlinks so path comparison works on systems where the temp
	// directory is behind a symlink (e.g., macOS /var → /private/var).
	canonicalWorktreeDir := p.worktreeDir
	if resolved, err := filepath.EvalSymlinks(p.worktreeDir); err == nil {
		canonicalWorktreeDir = resolved
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var curPath, curBranch string
	recovered := 0

	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			curPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			curBranch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "": // end of entry
			if curPath != "" && curBranch != "" && strings.HasPrefix(curBranch, "tack/") {
				// Only recover worktrees that live under our worktreeDir.
				rel, relErr := filepath.Rel(canonicalWorktreeDir, curPath)
				if relErr != nil || strings.HasPrefix(rel, "..") {
					curPath, curBranch = "", ""
					continue
				}
				// The sandbox ID is the directory name (UUID).
				id := filepath.Base(curPath)
				if _, exists := p.sandboxes[id]; exists {
					curPath, curBranch = "", ""
					continue
				}

				// Read full labels from persisted metadata file.
				labels := loadMeta(curPath)

				p.sandboxes[id] = &LocalSandbox{
					id:     id,
					path:   curPath,
					branch: curBranch,
					labels: labels,
					status: sandbox.SandboxStatusRunning,
				}
				recovered++
			}
			curPath, curBranch = "", ""
		}
	}

	if recovered > 0 {
		p.logger.Info("rediscovered sandbox worktrees", "count", recovered)
	}
}

// metaPath returns the path to the sandbox metadata file. The file is stored
// inside the worktree's private git directory (not the working tree) so it
// can never be staged or committed by agents, and won't cause merge conflicts.
func metaPath(worktreePath string) string {
	gitDirFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitDirFile)
	if err != nil {
		// Fall back to the working tree if .git isn't a file (shouldn't happen for worktrees).
		return filepath.Join(worktreePath, sandboxMetaFile)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	return filepath.Join(gitDir, sandboxMetaFile)
}

// persistMeta writes sandbox labels to a JSON file inside the worktree's
// private git directory so they can be recovered after a daemon restart.
func persistMeta(worktreePath string, labels map[string]string) error {
	data, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	path := metaPath(worktreePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadMeta reads sandbox labels from the metadata file. Returns an empty map
// on any error (best-effort recovery).
func loadMeta(worktreePath string) map[string]string {
	data, err := os.ReadFile(metaPath(worktreePath))
	if err != nil {
		return make(map[string]string)
	}
	var labels map[string]string
	if err := json.Unmarshal(data, &labels); err != nil {
		return make(map[string]string)
	}
	return labels
}

// SetProjectSetup configures project bootstrap for fresh worktrees.
func (p *Provider) SetProjectSetup(setup sandbox.ProjectSetup) {
	p.projectSetup = setup
}

// Create provisions a new git worktree sandbox.
func (p *Provider) Create(ctx context.Context, opts sandbox.CreateOpts) (sandbox.Sandbox, error) {
	id := uuid.New().String()

	// Use explicit branch name if provided; otherwise derive from labels.
	branch := opts.Branch
	if branch == "" {
		branch = fmt.Sprintf("%s-%s", sandbox.DeriveBranchPrefix(opts), id[:8])
	}

	// Validate branch name to prevent path traversal and injection.
	if strings.Contains(branch, "..") {
		return nil, fmt.Errorf("invalid branch name %q: contains '..'", branch)
	}
	if !validBranchRe.MatchString(branch) {
		return nil, fmt.Errorf("invalid branch name %q: must start with an alphanumeric character and contain only alphanumeric, hyphen, underscore, dot, or slash characters", branch)
	}

	worktreePath := filepath.Join(p.worktreeDir, id)

	p.logger.Info("creating sandbox worktree",
		"id", id,
		"branch", branch,
		"path", worktreePath,
	)

	// git worktree add {path} -b {branch} [baseRef]
	args := []string{"worktree", "add", worktreePath, "-b", branch}
	if opts.BaseRef != "" {
		args = append(args, opts.BaseRef)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = p.repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("creating git worktree: %w (stderr: %s)", err, stderr.String())
	}

	// Persist sandbox metadata for rediscovery after daemon restart.
	if err := persistMeta(worktreePath, opts.Labels); err != nil {
		p.logger.Warn("failed to persist sandbox metadata", "error", err)
	}

	// Copy gitignored files (node_modules, build caches, .env) from main repo.
	if !opts.SkipIgnoredCopy {
		if err := copyIgnoredFiles(p.repoRoot, worktreePath, p.logger); err != nil {
			p.logger.Warn("copy-ignored failed, continuing", "error", err)
		}
	}

	setupEnv := sandboxEnv(opts.EnvVars, nil)
	if err := sandbox.RunProjectSetup(ctx, p.projectSetup, func(ctx context.Context, command string) (sandbox.ExecResult, error) {
		c := exec.CommandContext(ctx, "sh", "-c", command)
		c.Dir = worktreePath
		c.Env = setupEnv
		out, err := c.CombinedOutput()
		result := sandbox.ExecResult{Stdout: strings.TrimSpace(string(out))}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		if err != nil {
			return result, err
		}
		return result, nil
	}, p.logger); err != nil {
		return nil, fmt.Errorf("running project setup: %w", err)
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

// matchesLabels delegates to the shared sandbox.MatchesLabels helper.
var matchesLabels = sandbox.MatchesLabels

// envAllowlist contains host environment variable names (or prefixes ending in
// '*') that are safe to pass into sandbox processes. Everything else is filtered.
var envAllowlist = []string{
	"PATH", "HOME", "USER", "SHELL",
	"LANG", "LC_*",
	"TERM", "TMPDIR",
	"XDG_*",
}

// sandboxEnv builds a process environment from the allowlist + sandbox-level
// env + per-execution env. No other host variables pass through.
func sandboxEnv(sandboxVars, execVars map[string]string) []string {
	env := filteredHostEnv()
	for k, v := range sandboxVars {
		env = append(env, k+"="+v)
	}
	for k, v := range execVars {
		env = append(env, k+"="+v)
	}
	return env
}

// filteredHostEnv returns only host env vars matching the allowlist.
func filteredHostEnv() []string {
	var result []string
	for _, entry := range os.Environ() {
		eqIdx := strings.IndexByte(entry, '=')
		if eqIdx < 0 {
			continue
		}
		name := entry[:eqIdx]
		if isAllowed(name) {
			result = append(result, entry)
		}
	}
	return result
}

func isAllowed(name string) bool {
	for _, pattern := range envAllowlist {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(name, pattern[:len(pattern)-1]) {
				return true
			}
		} else if name == pattern {
			return true
		}
	}
	return false
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
		resolved, err := resolveWithin(s.path, opts.WorkDir)
		if err != nil {
			return sandbox.ExecResult{}, err
		}
		workDir = resolved
	}
	cmd.Dir = workDir

	cmd.Env = sandboxEnv(s.envVars, opts.Env)

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
		resolved, err := resolveWithin(s.path, opts.WorkDir)
		if err != nil {
			return nil, err
		}
		workDir = resolved
	}
	cmd.Dir = workDir

	cmd.Env = sandboxEnv(s.envVars, opts.Env)

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
	// Pi can emit very large JSONL lines (e.g. file contents in tool results,
	// agent_end with full conversation history). 16MB handles planners.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

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
	dest, err := resolveWithin(s.path, path)
	if err != nil {
		return err
	}
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
	src, err := resolveWithin(s.path, path)
	if err != nil {
		return nil, err
	}
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
