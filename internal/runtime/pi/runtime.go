package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// writeExtensionToTemp writes the embedded extension files to a location
// appropriate for the sandbox type:
//   - Local sandbox: writes to an OS temp dir so the project tree stays clean.
//     Returns the absolute path to the temp dir.
//   - Remote sandbox (Daytona etc): uploads to .pi/extensions/deck-agent inside
//     the sandbox filesystem. Returns the relative path.
func (r *Runtime) writeExtensionToTemp(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	// Try local temp first — works for local sandbox provider.
	tmpDir, err := os.MkdirTemp("", "deck-pi-ext-*")
	if err != nil {
		// Fall back to uploading into sandbox (remote provider).
		return r.uploadExtensionToSandbox(ctx, sb, files)
	}

	for name, content := range files {
		dest := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			os.RemoveAll(tmpDir)
			return r.uploadExtensionToSandbox(ctx, sb, files)
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			os.RemoveAll(tmpDir)
			return r.uploadExtensionToSandbox(ctx, sb, files)
		}
	}

	return tmpDir, nil
}

func (r *Runtime) uploadExtensionToSandbox(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	extDir := ".pi/extensions/deck-agent"
	for name, content := range files {
		path := extDir + "/" + name
		if err := sb.Upload(ctx, content, path); err != nil {
			return "", fmt.Errorf("uploading extension file %s: %w", name, err)
		}
	}
	return extDir, nil
}

func (r *Runtime) Name() string        { return "pi" }
func (r *Runtime) SupportsRPC() bool   { return true }
func (r *Runtime) SupportsHooks() bool { return true }

// Spawn starts a Pi process in the sandbox.
// Flow:
//  1. Upload embedded extension to sandbox
//  2. Start Pi via ExecStreaming in RPC mode
//  3. Send initial prompt via RPC
func (r *Runtime) Spawn(ctx context.Context, sb sandbox.Sandbox, opts runtime.AgentOpts) (runtime.AgentProcess, error) {
	// 1. Write extension files to a temp directory (not the project tree).
	// For local sandboxes this avoids polluting the user's git repo with
	// runtime artifacts. For remote sandboxes we still upload into the
	// sandbox filesystem via sb.Upload.
	extFiles := ExtensionFiles()
	extDir, err := r.writeExtensionToTemp(ctx, sb, extFiles)
	if err != nil {
		return nil, fmt.Errorf("writing extension: %w", err)
	}
	r.logger.Info("pi extension ready", "files", len(extFiles), "dir", extDir)

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

	// Set cleanup to remove temp extension dir when process finishes
	if strings.HasPrefix(extDir, os.TempDir()) {
		cleanupDir := extDir
		proc.cleanup = func() {
			os.RemoveAll(cleanupDir)
			r.logger.Debug("cleaned up temp extension dir", "dir", cleanupDir)
		}
	}

	// 7. Send initial prompt via RPC
	prompt := opts.Overlay + "\n\nBegin your task now."
	initCmd := PiCommand{
		Type:    PiCommandPrompt,
		Message: prompt,
	}
	data, err := json.Marshal(initCmd)
	if err != nil {
		_ = handle.Kill()
		return nil, fmt.Errorf("marshaling initial prompt: %w", err)
	}
	data = append(data, '\n')
	if err := handle.Write(data); err != nil {
		_ = handle.Kill()
		return nil, fmt.Errorf("sending initial prompt: %w", err)
	}

	return proc, nil
}
