package claudecode

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
)

type testSandbox struct {
	cmd  string
	opts sandbox.ExecOpts
}

func (s *testSandbox) ID() string                    { return "test" }
func (s *testSandbox) Status() sandbox.SandboxStatus { return sandbox.SandboxStatusRunning }
func (s *testSandbox) Exec(_ context.Context, cmd string, opts sandbox.ExecOpts) (sandbox.ExecResult, error) {
	s.cmd = cmd
	s.opts = opts
	return sandbox.ExecResult{ExitCode: 0, Stdout: "done"}, nil
}
func (s *testSandbox) ExecStreaming(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ProcessHandle, error) {
	return nil, fmt.Errorf("unexpected ExecStreaming call")
}
func (s *testSandbox) Upload(_ context.Context, _ []byte, _ string) error { return nil }
func (s *testSandbox) Download(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (s *testSandbox) Stop(_ context.Context) error  { return nil }
func (s *testSandbox) Start(_ context.Context) error { return nil }
func (s *testSandbox) Health() error                 { return nil }

func TestSpawn_QuotesModelAndAllowedTools(t *testing.T) {
	r := New("", slog.Default())
	sb := &testSandbox{}
	model := "sonnet'; touch /tmp/pwned; echo '"
	tools := []string{"bash", "write"}

	proc, err := r.Spawn(context.Background(), sb, runtime.AgentOpts{
		Overlay: "test overlay",
		Model:   model,
		Tools:   tools,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if !strings.Contains(sb.cmd, "--model "+naming.ShellQuote(model)) {
		t.Fatalf("command %q missing quoted model", sb.cmd)
	}
	if !strings.Contains(sb.cmd, "--allowedTools "+naming.ShellQuote("bash,write")) {
		t.Fatalf("command %q missing quoted tool list", sb.cmd)
	}
}
