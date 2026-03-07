package gates

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syndg/deck/internal/sandbox"
)

const defaultTimeout = 120 * time.Second

// Runner executes quality gates inside a sandbox.
type Runner struct {
	logger *slog.Logger
}

// NewRunner creates a new gate runner.
func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{logger: logger}
}

// Run executes all gates sequentially in the given sandbox.
// Stops on first failure unless continueOnFailure is true.
// Each gate runs as a command via sandbox.Exec().
func (r *Runner) Run(ctx context.Context, sb sandbox.Sandbox, gates []Gate, continueOnFailure bool) (*RunResult, error) {
	result := &RunResult{
		AllPassed: true,
		Results:   make([]GateResult, 0, len(gates)),
	}

	for _, g := range gates {
		gr, err := r.RunSingle(ctx, sb, g)
		if err != nil {
			return nil, fmt.Errorf("running gate %q: %w", g.Name, err)
		}

		result.Results = append(result.Results, *gr)

		if !gr.Passed {
			result.AllPassed = false
			if !continueOnFailure {
				r.logger.Info("gate failed, stopping", "gate", g.Name, "exit_code", gr.ExitCode)
				break
			}
			r.logger.Info("gate failed, continuing", "gate", g.Name, "exit_code", gr.ExitCode)
		}
	}

	return result, nil
}

// RunSingle executes a single gate in the sandbox.
func (r *Runner) RunSingle(ctx context.Context, sb sandbox.Sandbox, gate Gate) (*GateResult, error) {
	timeout := defaultTimeout
	if gate.Timeout > 0 {
		timeout = time.Duration(gate.Timeout) * time.Second
	}

	opts := sandbox.ExecOpts{
		Timeout: timeout,
	}

	r.logger.Info("running gate", "gate", gate.Name, "command", gate.Command, "timeout", timeout)

	start := time.Now()
	execResult, err := sb.Exec(ctx, gate.Command, opts)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("executing command: %w", err)
	}

	gr := &GateResult{
		Gate:     gate,
		Passed:   execResult.ExitCode == 0,
		ExitCode: execResult.ExitCode,
		Stdout:   execResult.Stdout,
		Stderr:   execResult.Stderr,
		Duration: int(duration.Milliseconds()),
	}

	r.logger.Info("gate completed", "gate", gate.Name, "passed", gr.Passed, "duration_ms", gr.Duration)

	return gr, nil
}

// DefaultGates returns the default quality gates from config.
// Maps simple names to commands: "lint" -> the configured lint command, etc.
func DefaultGates(gateNames []string, commands map[string]string) []Gate {
	gates := make([]Gate, 0, len(gateNames))
	for _, name := range gateNames {
		cmd, ok := commands[name]
		if !ok {
			continue
		}
		gates = append(gates, Gate{
			Name:    name,
			Command: cmd,
		})
	}
	return gates
}
