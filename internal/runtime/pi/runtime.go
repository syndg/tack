package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
)

// Runtime spawns Pi agents in sandboxes with RPC-based communication.
type Runtime struct {
	model         string
	provider      string // LLM provider (default: "anthropic")
	thinkingLevel string // default: "medium"
	logger        *slog.Logger
}

// RuntimeConfig holds Pi-specific runtime configuration.
type RuntimeConfig struct {
	Model         string
	Provider      string
	ThinkingLevel string
}

func New(cfg RuntimeConfig, logger *slog.Logger) *Runtime {
	provider := cfg.Provider
	if provider == "" {
		provider = "anthropic"
	}
	thinkingLevel := cfg.ThinkingLevel
	if thinkingLevel == "" {
		thinkingLevel = "medium"
	}
	return &Runtime{
		model:         cfg.Model,
		provider:      provider,
		thinkingLevel: thinkingLevel,
		logger:        logger,
	}
}

func (r *Runtime) Name() string       { return "pi" }
func (r *Runtime) SupportsRPC() bool  { return true }
func (r *Runtime) SupportsHooks() bool { return true }

// Spawn starts a Pi process in the sandbox.
// Flow:
//  1. Upload embedded extension to sandbox
//  2. Start Pi via ExecStreaming in RPC mode
//  3. Send initial prompt via RPC
func (r *Runtime) Spawn(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	// 1. Upload extension files to sandbox
	extFiles := ExtensionFiles()
	extDir := ".pi/extensions/deck-agent"
	for name, content := range extFiles {
		path := extDir + "/" + name
		if err := sb.Upload(ctx, content, path); err != nil {
			return nil, fmt.Errorf("uploading extension file %s: %w", name, err)
		}
	}
	r.logger.Info("uploaded pi extension", "files", len(extFiles), "dir", extDir)

	// 2. Determine model
	model := r.model
	if opts.Model != "" {
		model = opts.Model
	}

	// 3. Build Pi command with RPC mode
	cmdParts := []string{
		"pi",
		"--mode", "rpc",
		"--provider", r.provider,
		"--thinking", r.thinkingLevel,
	}
	if model != "" {
		cmdParts = append(cmdParts, "--model", model)
	}
	cmdParts = append(cmdParts, "--extension", extDir)
	cmd := strings.Join(cmdParts, " ")

	// 4. Set up env vars
	env := make(map[string]string)
	for k, v := range opts.EnvVars {
		env[k] = v
	}

	// 5. Start streaming process
	handle, err := sb.ExecStreaming(ctx, cmd, sandbox.ExecOpts{
		WorkDir: opts.WorkDir,
		Env:     env,
	})
	if err != nil {
		return nil, fmt.Errorf("starting pi process: %w", err)
	}

	r.logger.Info("spawning pi agent", "model", model, "provider", r.provider, "thinking", r.thinkingLevel)

	// 6. Create process with output channel
	proc := newPiProcess(handle, r.logger)

	// 7. Send initial prompt via RPC
	prompt := opts.Overlay + "\n\nBegin your task now."
	initCmd := PiCommand{
		Type:    PiCommandPrompt,
		Content: prompt,
	}
	data, err := json.Marshal(initCmd)
	if err != nil {
		handle.Kill()
		return nil, fmt.Errorf("marshaling initial prompt: %w", err)
	}
	data = append(data, '\n')
	if err := handle.Write(data); err != nil {
		handle.Kill()
		return nil, fmt.Errorf("sending initial prompt: %w", err)
	}

	return proc, nil
}
