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

// writeExtension uploads extension files into the sandbox filesystem via sb.Upload.
// Uses .deck-ext/ inside the sandbox so the extension is accessible from the
// sandbox process regardless of whether it's local or remote.
// The worktree (and .deck-ext/ with it) is cleaned up when the objective completes.
func (r *Runtime) writeExtension(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	return r.uploadExtensionToSandbox(ctx, sb, files)
}

func (r *Runtime) uploadExtensionToSandbox(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	extDir := ".deck-ext"
	for name, content := range files {
		path := extDir + "/" + name
		if err := sb.Upload(ctx, content, path); err != nil {
			return "", fmt.Errorf("uploading extension file %s: %w", name, err)
		}
	}

	// Exclude .deck-ext from git inside the sandbox so auto-commit doesn't
	// include runtime artifacts. Appends to .git/info/exclude which is outside
	// the working tree and won't show up as a change.
	// Commands are broken into separate calls because Daytona's ExecuteCommand
	// uses exec.Command directly (no shell) — &&, ||, $() don't work.
	r.addGitExclude(ctx, sb, extDir)

	return extDir, nil
}

// addGitExclude appends a path to .git/info/exclude if not already present.
// Each step is a separate sb.Exec call for Daytona compatibility (no shell).
func (r *Runtime) addGitExclude(ctx context.Context, sb sandbox.Sandbox, pattern string) {
	// 1. Get the git directory path.
	res, err := sb.Exec(ctx, "git rev-parse --git-dir", sandbox.ExecOpts{})
	if err != nil || res.ExitCode != 0 {
		r.logger.Warn("failed to get git dir for exclude", "error", err)
		return
	}
	gitDir := strings.TrimSpace(res.Stdout)
	if gitDir == "" {
		return
	}

	infoDir := gitDir + "/info"
	excludePath := infoDir + "/exclude"

	// 2. Ensure info directory exists.
	sb.Exec(ctx, fmt.Sprintf("mkdir -p %s", infoDir), sandbox.ExecOpts{})

	// 3. Check if pattern is already excluded.
	checkRes, _ := sb.Exec(ctx, fmt.Sprintf("grep -q %s %s", pattern, excludePath), sandbox.ExecOpts{})
	if checkRes.ExitCode == 0 {
		return // already excluded
	}

	// 4. Append the pattern. Use Upload since >> redirect doesn't work
	// with Daytona's exec.Command (no shell).
	// Read existing content first, then upload with the new line appended.
	catRes, _ := sb.Exec(ctx, fmt.Sprintf("cat %s", excludePath), sandbox.ExecOpts{})
	existing := ""
	if catRes.ExitCode == 0 {
		existing = catRes.Stdout
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	if err := sb.Upload(ctx, []byte(existing+pattern+"\n"), excludePath); err != nil {
		r.logger.Warn("failed to write git exclude", "error", err)
	}
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
	extDir, err := r.writeExtension(ctx, sb, extFiles)
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
