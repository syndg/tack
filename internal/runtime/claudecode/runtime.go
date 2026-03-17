package claudecode

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/syndg/deck/internal/naming"
	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
)

// Runtime spawns Claude Code agents in sandboxes via `claude -p`.
type Runtime struct {
	model  string // default model (e.g., "sonnet")
	logger *slog.Logger
}

func New(model string, logger *slog.Logger) *Runtime {
	return &Runtime{
		model:  model,
		logger: logger,
	}
}

func (r *Runtime) Name() string        { return "claude-code" }
func (r *Runtime) SupportsRPC() bool   { return false }
func (r *Runtime) SupportsHooks() bool { return true }

// Spawn starts a Claude Code process in the sandbox.
func (r *Runtime) Spawn(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	execCtx, cancel := context.WithCancel(ctx)

	// 1. Build prompt
	prompt := opts.Overlay + "\n\nBegin your task now."

	// 2. Build tool allowlist
	toolList := strings.Join(opts.Tools, ",")

	// 3. Determine model
	model := r.model
	if opts.Model != "" {
		model = opts.Model
	}

	// 4. Construct command: shell-quote the prompt using single quotes.
	// --dangerously-skip-permissions is safe here because agents run in
	// isolated worktrees with no internet access or sensitive data.
	quotedPrompt := naming.ShellQuote(prompt)
	cmd := fmt.Sprintf("claude -p %s --model %s --dangerously-skip-permissions", quotedPrompt, model)
	if toolList != "" {
		cmd += " --allowedTools " + toolList
	}

	// 5. Set up env vars
	env := make(map[string]string)
	if daemonURL, ok := opts.EnvVars["DECK_DAEMON_URL"]; ok {
		env["DECK_DAEMON_URL"] = daemonURL
	}
	if agentToken, ok := opts.EnvVars["DECK_AGENT_TOKEN"]; ok {
		env["DECK_AGENT_TOKEN"] = agentToken
	}
	for k, v := range opts.EnvVars {
		env[k] = v
	}

	outputCh := make(chan runtime.AgentEvent, 16)
	doneCh := make(chan struct{})

	proc := &ClaudeCodeProcess{
		sandbox:  sb,
		cancel:   cancel,
		doneCh:   doneCh,
		outputCh: outputCh,
	}

	// 6. Execute in goroutine
	go func() {
		defer close(doneCh)
		defer close(outputCh)

		execOpts := sandbox.ExecOpts{
			WorkDir: opts.WorkDir,
			Env:     env,
		}

		r.logger.Info("spawning claude code agent", "model", model, "tools", toolList)

		result, err := sb.Exec(execCtx, cmd, execOpts)

		proc.mu.Lock()
		defer proc.mu.Unlock()

		if err != nil {
			proc.result = runtime.AgentResult{
				Success: false,
				Error:   fmt.Sprintf("exec error: %v", err),
			}
			select {
			case outputCh <- runtime.AgentEvent{Type: "error", Content: proc.result.Error}:
			default:
			}
			return
		}

		// 7. Parse exit code, set result
		success := result.ExitCode == 0
		agentResult := runtime.AgentResult{
			Success: success,
			Summary: result.Stdout,
		}
		if !success {
			agentResult.Error = fmt.Sprintf("exit code %d: %s", result.ExitCode, result.Stderr)
		}
		proc.result = agentResult

		// Emit output event with stdout
		if result.Stdout != "" {
			select {
			case outputCh <- runtime.AgentEvent{Type: "output", Content: result.Stdout}:
			default:
			}
		}
	}()

	// 8. Return process immediately
	return proc, nil
}

// ClaudeCodeProcess implements runtime.AgentProcess.
type ClaudeCodeProcess struct {
	sandbox  sandbox.Sandbox
	cancel   context.CancelFunc
	doneCh   chan struct{}
	result   runtime.AgentResult
	outputCh chan runtime.AgentEvent
	mu       sync.Mutex
	killed   bool
}

// Send is a no-op for Claude Code (no mid-execution RPC support).
func (p *ClaudeCodeProcess) Send(_ context.Context, _ runtime.AgentMessage) error {
	return nil
}

// Output returns the channel that receives agent events.
func (p *ClaudeCodeProcess) Output() <-chan runtime.AgentEvent {
	return p.outputCh
}

// Wait blocks until the Claude Code process completes and returns the result.
func (p *ClaudeCodeProcess) Wait() (runtime.AgentResult, error) {
	<-p.doneCh
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result, nil
}

// Kill terminates the process by cancelling the context.
func (p *ClaudeCodeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.killed {
		p.killed = true
		p.cancel()
	}
	return nil
}
