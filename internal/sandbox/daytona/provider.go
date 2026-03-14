package daytona

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	daytona "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/options"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"

	"github.com/syndg/deck/internal/sandbox"
)

// Provider creates sandboxes via the Daytona SDK.
type Provider struct {
	client    *daytona.Client
	cfg       Config
	mu        sync.Mutex
	sandboxes map[string]*DaytonaSandbox
	logger    *slog.Logger
}

// Config holds Daytona connection settings.
type Config struct {
	APIKey   string
	APIURL   string
	Snapshot string
}

func New(cfg Config, logger *slog.Logger) (*Provider, error) {
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

	return &Provider{
		client:    client,
		cfg:       cfg,
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

	sb := &DaytonaSandbox{
		sandbox: dSandbox,
		labels:  opts.Labels,
		envVars: opts.EnvVars,
		logger:  p.logger,
	}

	p.mu.Lock()
	p.sandboxes[dSandbox.ID] = sb
	p.mu.Unlock()

	return sb, nil
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
		sandbox: dSandbox,
		logger:  p.logger,
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
				sandbox: dSandbox,
				labels:  labels, // API already filtered by these labels.
				logger:  p.logger,
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

func matchesLabels(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
			return false
		}
	}
	return true
}

// buildEffectiveCommand prepends env exports to a command when the Daytona
// toolbox API doesn't support per-command env vars natively.
func buildEffectiveCommand(cmd string, opts sandbox.ExecOpts) string {
	if len(opts.Env) == 0 {
		return cmd
	}
	var exports []string
	for k, v := range opts.Env {
		exports = append(exports, fmt.Sprintf("export %s=%s", k, shellescape(v)))
	}
	return strings.Join(exports, " && ") + " && " + cmd
}

// shellescape wraps a value in single quotes for safe shell interpolation.
func shellescape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DaytonaSandbox wraps a Daytona SDK sandbox to implement sandbox.Sandbox.
type DaytonaSandbox struct {
	sandbox *daytona.Sandbox
	labels  map[string]string
	envVars map[string]string
	logger  *slog.Logger
}

func (s *DaytonaSandbox) ID() string {
	return s.sandbox.ID
}

func (s *DaytonaSandbox) Status() sandbox.SandboxStatus {
	return sandbox.SandboxStatusRunning
}

func (s *DaytonaSandbox) Exec(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	// Build the effective command with env and workdir from ExecOpts.
	effectiveCmd := buildEffectiveCommand(cmd, opts)

	var execOpts []func(*options.ExecuteCommand)
	if opts.WorkDir != "" {
		execOpts = append(execOpts, options.WithCwd(opts.WorkDir))
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
	sessionID := fmt.Sprintf("deck-%d", time.Now().UnixNano())

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

	// Build command with cd prefix if WorkDir is set.
	effectiveCmd := cmd
	if opts.WorkDir != "" {
		effectiveCmd = fmt.Sprintf("cd %s && %s", shellescape(opts.WorkDir), cmd)
	}

	// Send the actual command to the PTY shell.
	if err := pty.SendInput([]byte(effectiveCmd + "\n")); err != nil {
		pty.Disconnect()
		return nil, fmt.Errorf("sending command to PTY: %w", err)
	}

	return &daytonaPtyHandle{
		pty:     pty,
		scanner: bufio.NewScanner(pty),
	}, nil
}

func (s *DaytonaSandbox) Upload(ctx context.Context, content []byte, path string) error {
	// UploadFile accepts []byte or string (local file path) as source
	return s.sandbox.FileSystem.UploadFile(ctx, content, path)
}

func (s *DaytonaSandbox) Download(ctx context.Context, path string) ([]byte, error) {
	return s.sandbox.FileSystem.DownloadFile(ctx, path, nil)
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
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func (h *daytonaPtyHandle) Write(data []byte) error {
	return h.pty.SendInput(data)
}

func (h *daytonaPtyHandle) ReadLine() (string, error) {
	if h.scanner.Scan() {
		line := h.scanner.Text()
		line = ansiEscape.ReplaceAllString(line, "")
		return line, nil
	}
	if err := h.scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
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
