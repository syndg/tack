package pi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
)

// mockProcessHandle simulates a ProcessHandle for testing.
type mockProcessHandle struct {
	written []string
	lines   []string
	lineIdx int
	killed  bool
}

func (h *mockProcessHandle) Write(data []byte) error {
	h.written = append(h.written, string(data))
	return nil
}

func (h *mockProcessHandle) ReadLine() (string, error) {
	if h.lineIdx >= len(h.lines) {
		return "", io.EOF
	}
	line := h.lines[h.lineIdx]
	h.lineIdx++
	return line, nil
}

func (h *mockProcessHandle) Wait() (int, error) { return 0, nil }
func (h *mockProcessHandle) Kill() error        { h.killed = true; return nil }

// mockSandbox for Pi runtime tests.
type mockSandbox struct {
	uploaded   map[string][]byte
	execCmds   []string // commands passed to Exec
	streamFn   func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ProcessHandle, error)
	streamCmd  string
	streamOpts sandbox.ExecOpts
}

func newMockSandbox() *mockSandbox {
	return &mockSandbox{uploaded: make(map[string][]byte)}
}

func (m *mockSandbox) ID() string                    { return "mock-sb" }
func (m *mockSandbox) Status() sandbox.SandboxStatus { return sandbox.SandboxStatusRunning }
func (m *mockSandbox) Exec(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
	m.execCmds = append(m.execCmds, cmd)
	// Simulate git rev-parse --git-dir for addGitExclude.
	if strings.Contains(cmd, "rev-parse --git-dir") {
		return sandbox.ExecResult{ExitCode: 0, Stdout: ".git"}, nil
	}
	// grep for exclude pattern — return exit 1 (not found) so the upload path runs.
	if strings.Contains(cmd, "grep") {
		return sandbox.ExecResult{ExitCode: 1}, nil
	}
	return sandbox.ExecResult{ExitCode: 0, Stdout: "ok"}, nil
}
func (m *mockSandbox) ExecStreaming(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	m.streamCmd = cmd
	m.streamOpts = opts
	if m.streamFn != nil {
		return m.streamFn(ctx, cmd, opts)
	}
	return nil, fmt.Errorf("ExecStreaming not configured")
}
func (m *mockSandbox) Upload(_ context.Context, content []byte, path string) error {
	m.uploaded[path] = content
	return nil
}
func (m *mockSandbox) Download(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (m *mockSandbox) Stop(_ context.Context) error                         { return nil }
func (m *mockSandbox) Start(_ context.Context) error                        { return nil }
func (m *mockSandbox) Health() error                                        { return nil }

func TestRuntime_Name(t *testing.T) {
	r := New(RuntimeConfig{}, slog.Default())
	if r.Name() != "pi" {
		t.Errorf("Name() = %q, want pi", r.Name())
	}
}

func TestRuntime_SupportsRPC(t *testing.T) {
	r := New(RuntimeConfig{}, slog.Default())
	if !r.SupportsRPC() {
		t.Error("SupportsRPC() should return true")
	}
}

func TestRuntime_SupportsHooks(t *testing.T) {
	r := New(RuntimeConfig{}, slog.Default())
	if !r.SupportsHooks() {
		t.Error("SupportsHooks() should return true")
	}
}

func TestRuntime_Spawn_UploadsExtension(t *testing.T) {
	r := New(RuntimeConfig{Model: "test-model"}, slog.Default())
	sb := newMockSandbox()

	handle := &mockProcessHandle{
		lines: []string{`{"type":"agent_end"}`},
	}
	sb.streamFn = func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
		return handle, nil
	}

	proc, err := r.Spawn(context.Background(), sb, runtime.AgentOpts{
		Overlay: "test overlay",
		EnvVars: map[string]string{"TACK_DAEMON_URL": "http://localhost:9800"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Extension files should be uploaded to the sandbox via sb.Upload.
	if len(sb.uploaded) == 0 {
		t.Error("expected extension files to be uploaded to sandbox")
	}
	foundIndex := false
	for path := range sb.uploaded {
		if strings.Contains(path, "index.ts") {
			foundIndex = true
		}
	}
	if !foundIndex {
		t.Error("expected index.ts to be uploaded")
	}

	// Check command contains pi --mode rpc
	if !strings.Contains(sb.streamCmd, "pi") {
		t.Errorf("command %q should contain pi", sb.streamCmd)
	}
	if !strings.Contains(sb.streamCmd, "--mode rpc") {
		t.Errorf("command %q should contain --mode rpc", sb.streamCmd)
	}
	if !strings.Contains(sb.streamCmd, "--model test-model") {
		t.Errorf("command %q should contain --model test-model", sb.streamCmd)
	}

	// Initial prompt should have been written
	if len(handle.written) == 0 {
		t.Error("expected initial prompt to be sent")
	}
	if !strings.Contains(handle.written[0], "test overlay") {
		t.Errorf("initial prompt %q should contain overlay", handle.written[0])
	}

	// Wait for process to finish
	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %+v", result)
	}

	// Should have uploaded .git/info/exclude with .tack-ext pattern
	excludeContent, ok := sb.uploaded[".git/info/exclude"]
	if !ok {
		t.Error("expected .git/info/exclude to be uploaded with .tack-ext pattern")
	} else if !strings.Contains(string(excludeContent), ".tack-ext") {
		t.Errorf("exclude file content %q should contain .tack-ext", string(excludeContent))
	}
}

func TestRuntime_Spawn_ModelOverride(t *testing.T) {
	r := New(RuntimeConfig{Model: "default-model"}, slog.Default())
	sb := newMockSandbox()

	handle := &mockProcessHandle{
		lines: []string{`{"type":"agent_end"}`},
	}
	sb.streamFn = func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
		return handle, nil
	}

	_, err := r.Spawn(context.Background(), sb, runtime.AgentOpts{
		Overlay: "test",
		Model:   "override-model",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if !strings.Contains(sb.streamCmd, "--model override-model") {
		t.Errorf("command %q should use override model", sb.streamCmd)
	}
}

func TestRuntime_Spawn_EnvVarsPassedThrough(t *testing.T) {
	r := New(RuntimeConfig{}, slog.Default())
	sb := newMockSandbox()

	handle := &mockProcessHandle{
		lines: []string{`{"type":"agent_end"}`},
	}
	sb.streamFn = func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
		return handle, nil
	}

	envVars := map[string]string{
		"TACK_DAEMON_URL":  "http://localhost:9800",
		"TACK_AGENT_TOKEN": "token-123",
	}
	_, err := r.Spawn(context.Background(), sb, runtime.AgentOpts{
		Overlay: "test",
		EnvVars: envVars,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if sb.streamOpts.Env["TACK_DAEMON_URL"] != "http://localhost:9800" {
		t.Error("TACK_DAEMON_URL not passed through")
	}
	if sb.streamOpts.Env["TACK_AGENT_TOKEN"] != "token-123" {
		t.Error("TACK_AGENT_TOKEN not passed through")
	}
}
