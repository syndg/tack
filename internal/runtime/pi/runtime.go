package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
)

// Runtime spawns Pi agents in sandboxes with RPC-based communication.
type Runtime struct {
	model         string
	provider      string // LLM provider (default: "anthropic")
	thinkingLevel string // default: "medium"
	logger        *slog.Logger
}

const sandboxPIBinary = ".tack-tools/pi/node_modules/.bin/pi"

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
// Uses .tack-ext/ inside the sandbox so the extension is accessible from the
// sandbox process regardless of whether it's local or remote.
// The worktree (and .tack-ext/ with it) is cleaned up when the objective completes.
func (r *Runtime) writeExtension(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	return r.uploadExtensionToSandbox(ctx, sb, files)
}

func (r *Runtime) uploadExtensionToSandbox(ctx context.Context, sb sandbox.Sandbox, files map[string][]byte) (string, error) {
	extDir := ".tack-ext"
	for name, content := range files {
		path := extDir + "/" + name
		if err := sb.Upload(ctx, content, path); err != nil {
			return "", fmt.Errorf("uploading extension file %s: %w", name, err)
		}
	}

	// Exclude .tack-ext from git inside the sandbox so auto-commit doesn't
	// include runtime artifacts. Use a rooted directory pattern so worktree
	// sandboxes ignore the full extension directory reliably.
	// Commands are broken into separate calls because Daytona's ExecuteCommand
	// uses exec.Command directly (no shell) — &&, ||, $() don't work.
	r.addGitExclude(ctx, sb, "/"+extDir+"/")

	return extDir, nil
}

// addGitExclude appends a path to the repository's effective info/exclude if
// not already present. In git worktrees this must use `git rev-parse --git-path
// info/exclude`, not `--git-dir`, because the shared exclude file lives under
// the common git dir.
func (r *Runtime) addGitExclude(ctx context.Context, sb sandbox.Sandbox, pattern string) {
	// 1. Get the effective exclude path.
	res, err := sb.Exec(ctx, "git rev-parse --git-path info/exclude", sandbox.ExecOpts{})
	if err != nil || res.ExitCode != 0 {
		r.logger.Warn("failed to get git exclude path", "error", err)
		return
	}
	excludePath := strings.TrimSpace(res.Stdout)
	if excludePath == "" {
		return
	}

	// 2. Ensure the parent directory exists.
	sb.Exec(ctx, fmt.Sprintf("mkdir -p %s", naming.ShellQuote(filepath.Dir(excludePath))), sandbox.ExecOpts{})

	// 3. Check if pattern is already excluded.
	checkRes, _ := sb.Exec(ctx, fmt.Sprintf("grep -Fqx %s %s", naming.ShellQuote(pattern), naming.ShellQuote(excludePath)), sandbox.ExecOpts{})
	if checkRes.ExitCode == 0 {
		return // already excluded
	}

	// 4. Append the pattern. Use Upload since >> redirect doesn't work
	// with Daytona's exec.Command (no shell).
	catRes, _ := sb.Exec(ctx, fmt.Sprintf("cat %s", naming.ShellQuote(excludePath)), sandbox.ExecOpts{})
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
	piCommand, err := r.ensurePIInstalled(ctx, sb)
	if err != nil {
		return nil, err
	}

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
		naming.ShellQuote(piCommand),
		"--mode", naming.ShellQuote("rpc"),
		"--provider", naming.ShellQuote(r.provider),
		"--thinking", naming.ShellQuote(r.thinkingLevel),
	}
	if model != "" {
		cmdParts = append(cmdParts, "--model", naming.ShellQuote(model))
	}
	cmdParts = append(cmdParts, "--extension", naming.ShellQuote(extDir))
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

func (r *Runtime) ensurePIInstalled(ctx context.Context, sb sandbox.Sandbox) (string, error) {
	// Prefer a sandbox-managed Pi install so remote sandboxes work without user-defined post_create.
	check, err := sb.Exec(ctx, "if [ -x "+naming.ShellQuote(sandboxPIBinary)+" ]; then echo local; elif command -v pi >/dev/null 2>&1; then echo global; else echo missing; fi", sandbox.ExecOpts{})
	if err == nil && check.ExitCode == 0 {
		switch strings.TrimSpace(check.Stdout) {
		case "local":
			r.addGitExclude(ctx, sb, "/.tack-tools/")
			return sandboxPIBinary, nil
		case "global":
			return "pi", nil
		}
	}

	r.logger.Info("pi runtime missing in sandbox, installing internal runtime bootstrap")
	installCmd := "mkdir -p .tack-tools/pi && " +
		"if command -v npm >/dev/null 2>&1; then " +
		"npm install --no-save --silent --prefix .tack-tools/pi @mariozechner/pi-coding-agent; " +
		"else echo 'npm is required to install pi in this sandbox' >&2; exit 1; fi"
	result, err := sb.Exec(ctx, installCmd, sandbox.ExecOpts{})
	if err != nil {
		return "", fmt.Errorf("installing pi runtime in sandbox: %w", err)
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(result.Stdout)
		}
		return "", fmt.Errorf("installing pi runtime in sandbox failed: %s", stderr)
	}
	r.addGitExclude(ctx, sb, "/.tack-tools/")
	return sandboxPIBinary, nil
}
