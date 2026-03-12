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

	daytona "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
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

	p.logger.Info("deleting daytona sandbox", "id", id)
	return sb.sandbox.Delete(ctx)
}

func matchesLabels(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
			return false
		}
	}
	return true
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
	resp, err := s.sandbox.Process.ExecuteCommand(ctx, cmd)
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("executing command: %w", err)
	}

	return sandbox.ExecResult{
		ExitCode: resp.ExitCode,
		Stdout:   strings.TrimRight(resp.Result, "\n"),
	}, nil
}

func (s *DaytonaSandbox) ExecStreaming(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	pty, err := s.sandbox.Process.CreatePty(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("creating PTY: %w", err)
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
