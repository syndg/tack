package gates

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/syndg/tack/internal/sandbox"
)

// mockSandbox implements sandbox.Sandbox for testing.
type mockSandbox struct {
	execFn func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error)
}

func (m *mockSandbox) ID() string                                           { return "mock-sb" }
func (m *mockSandbox) Status() sandbox.SandboxStatus                        { return sandbox.SandboxStatusRunning }
func (m *mockSandbox) Upload(ctx context.Context, c []byte, p string) error { return nil }
func (m *mockSandbox) Download(ctx context.Context, p string) ([]byte, error) {
	return nil, nil
}
func (m *mockSandbox) Stop(ctx context.Context) error  { return nil }
func (m *mockSandbox) Start(ctx context.Context) error { return nil }
func (m *mockSandbox) Exec(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	return m.execFn(ctx, cmd, opts)
}
func (m *mockSandbox) ExecStreaming(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	return nil, fmt.Errorf("ExecStreaming not implemented in mock")
}
func (m *mockSandbox) Health() error { return nil }

func TestRunSingle_Success(t *testing.T) {
	sb := &mockSandbox{
		execFn: func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 0, Stdout: "ok"}, nil
		},
	}

	runner := NewRunner(slog.Default())
	gate := Gate{Name: "lint", Command: "golangci-lint run", Timeout: 30}

	result, err := runner.RunSingle(context.Background(), sb, gate)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if !result.Passed {
		t.Error("expected gate to pass")
	}
	if result.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "ok" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "ok")
	}
	if result.Duration < 0 {
		t.Error("expected non-negative duration")
	}
}

func TestRunSingle_Failure(t *testing.T) {
	sb := &mockSandbox{
		execFn: func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 1, Stderr: "errors found"}, nil
		},
	}

	runner := NewRunner(slog.Default())
	gate := Gate{Name: "test", Command: "go test ./..."}

	result, err := runner.RunSingle(context.Background(), sb, gate)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if result.Passed {
		t.Error("expected gate to fail")
	}
	if result.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", result.ExitCode)
	}
}

func TestRun_StopsOnFirstFailure(t *testing.T) {
	callCount := 0
	sb := &mockSandbox{
		execFn: func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
			callCount++
			if cmd == "fail-cmd" {
				return sandbox.ExecResult{ExitCode: 1}, nil
			}
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}

	runner := NewRunner(slog.Default())
	gates := []Gate{
		{Name: "pass1", Command: "pass-cmd"},
		{Name: "fail", Command: "fail-cmd"},
		{Name: "pass2", Command: "pass-cmd"},
	}

	result, err := runner.Run(context.Background(), sb, gates, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.AllPassed {
		t.Error("expected AllPassed = false")
	}
	if len(result.Results) != 2 {
		t.Errorf("got %d results, want 2 (should stop after failure)", len(result.Results))
	}
	if callCount != 2 {
		t.Errorf("sandbox called %d times, want 2", callCount)
	}
}

func TestRun_ContinueOnFailure(t *testing.T) {
	sb := &mockSandbox{
		execFn: func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
			if cmd == "fail-cmd" {
				return sandbox.ExecResult{ExitCode: 1}, nil
			}
			return sandbox.ExecResult{ExitCode: 0}, nil
		},
	}

	runner := NewRunner(slog.Default())
	gates := []Gate{
		{Name: "pass1", Command: "pass-cmd"},
		{Name: "fail", Command: "fail-cmd"},
		{Name: "pass2", Command: "pass-cmd"},
	}

	result, err := runner.Run(context.Background(), sb, gates, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.AllPassed {
		t.Error("expected AllPassed = false")
	}
	if len(result.Results) != 3 {
		t.Errorf("got %d results, want 3 (should continue after failure)", len(result.Results))
	}
}

func TestRun_ExecError(t *testing.T) {
	sb := &mockSandbox{
		execFn: func(ctx context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{}, fmt.Errorf("sandbox connection lost")
		},
	}

	runner := NewRunner(slog.Default())
	gates := []Gate{
		{Name: "test", Command: "go test"},
	}

	_, err := runner.Run(context.Background(), sb, gates, false)
	if err == nil {
		t.Fatal("expected error from sandbox exec failure")
	}
}
