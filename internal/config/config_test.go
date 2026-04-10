package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := Load("", missing)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Daemon.Listen != "127.0.0.1:9800" {
		t.Fatalf("unexpected default listen: %q", cfg.Daemon.Listen)
	}
}

func TestLoadFileMergesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("daemon:\n  listen: 127.0.0.1:9999\nplanning:\n  model: custom-model\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load("", path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Daemon.Listen != "127.0.0.1:9999" {
		t.Fatalf("unexpected listen: %q", cfg.Daemon.Listen)
	}
	if cfg.Planning.Model != "custom-model" {
		t.Fatalf("unexpected model: %q", cfg.Planning.Model)
	}
	if cfg.Agents.Runtime != "claude-code" {
		t.Fatalf("expected default runtime to remain, got %q", cfg.Agents.Runtime)
	}
}

func TestExpandPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cfg := Default()
	cfg.Daemon.DataDir = "~/.tack/test-data"
	cfg.ExpandPaths()

	want := filepath.Join(home, ".tack/test-data")
	if cfg.Daemon.DataDir != want {
		t.Fatalf("expected %q, got %q", want, cfg.Daemon.DataDir)
	}
}

func TestLoad_SetsProjectRootFromProjectConfig(t *testing.T) {
	dir := t.TempDir()
	projPath := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(projPath, []byte("agents:\n  runtime: pi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(projPath, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProjectRoot != dir {
		t.Fatalf("ProjectRoot = %q, want %q", cfg.ProjectRoot, dir)
	}
}

func TestLayeredLoad_ProjectWinsOverUser(t *testing.T) {
	dir := t.TempDir()

	// User config: sets runtime and listen
	userPath := filepath.Join(dir, "user.yaml")
	os.WriteFile(userPath, []byte("agents:\n  runtime: pi\ndaemon:\n  listen: 127.0.0.1:8000\n"), 0o644)

	// Project config: overrides runtime, leaves listen alone
	projPath := filepath.Join(dir, "project.yaml")
	os.WriteFile(projPath, []byte("agents:\n  runtime: claude-code\nplanning:\n  model: opus-4\n"), 0o644)

	cfg, err := Load(projPath, userPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Project wins on runtime
	if cfg.Agents.Runtime != "claude-code" {
		t.Errorf("runtime = %q, want claude-code (project wins)", cfg.Agents.Runtime)
	}
	// User value survives where project doesn't set it
	if cfg.Daemon.Listen != "127.0.0.1:8000" {
		t.Errorf("listen = %q, want 127.0.0.1:8000 (user fallback)", cfg.Daemon.Listen)
	}
	// Project-only value
	if cfg.Planning.Model != "opus-4" {
		t.Errorf("model = %q, want opus-4", cfg.Planning.Model)
	}
	// Default survives
	if cfg.Agents.MaxConcurrent != 8 {
		t.Errorf("max_concurrent = %d, want 8 (default)", cfg.Agents.MaxConcurrent)
	}
}

func TestEffectiveModelsPreferExplicitModelConfig(t *testing.T) {
	cfg := Default()
	cfg.Models.Default = "gpt-default"
	cfg.Models.Agent = "gpt-agent"
	cfg.Models.Planner = "gpt-planner"
	cfg.Models.SmallTasks = "gpt-small"
	cfg.Models.Deterministic = "gpt-deterministic"
	cfg.Agents.Pi.Model = "legacy-pi"
	cfg.Planning.Model = "legacy-planning"

	if got := cfg.EffectiveAgentModel(); got != "gpt-agent" {
		t.Fatalf("EffectiveAgentModel = %q, want gpt-agent", got)
	}
	if got := cfg.EffectivePlannerModel(); got != "gpt-planner" {
		t.Fatalf("EffectivePlannerModel = %q, want gpt-planner", got)
	}
	if got := cfg.EffectiveDeterministicModel(); got != "gpt-deterministic" {
		t.Fatalf("EffectiveDeterministicModel = %q, want gpt-deterministic", got)
	}
	if got := cfg.EffectiveSmallTaskModel(); got != "gpt-small" {
		t.Fatalf("EffectiveSmallTaskModel = %q, want gpt-small", got)
	}
}

func TestEffectiveModelsFallBackToLegacyConfig(t *testing.T) {
	cfg := Default()
	cfg.Models = ModelsConfig{}
	cfg.Agents.Pi.Model = "legacy-pi"
	cfg.Planning.Model = "legacy-planning"

	if got := cfg.EffectiveAgentModel(); got != "legacy-pi" {
		t.Fatalf("EffectiveAgentModel = %q, want legacy-pi", got)
	}
	if got := cfg.EffectivePlannerModel(); got != "legacy-planning" {
		t.Fatalf("EffectivePlannerModel = %q, want legacy-planning", got)
	}
	if got := cfg.EffectiveDeterministicModel(); got != "legacy-planning" {
		t.Fatalf("EffectiveDeterministicModel = %q, want legacy-planning", got)
	}
	if got := cfg.EffectiveSmallTaskModel(); got != "legacy-planning" {
		t.Fatalf("EffectiveSmallTaskModel = %q, want legacy-planning", got)
	}
}

func TestEffectiveRuntimeAuthDefaults(t *testing.T) {
	cfg := Default()
	binding := cfg.EffectiveRuntimeAuth()
	if binding.Mode != "tack" {
		t.Fatalf("Mode = %q, want tack", binding.Mode)
	}
	if binding.Method != "api_key" {
		t.Fatalf("Method = %q, want api_key", binding.Method)
	}
	if binding.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want anthropic", binding.Provider)
	}
	if binding.CredentialRef != "anthropic" {
		t.Fatalf("CredentialRef = %q, want anthropic", binding.CredentialRef)
	}
	if binding.Runtime != cfg.Agents.Runtime {
		t.Fatalf("Runtime = %q, want %q", binding.Runtime, cfg.Agents.Runtime)
	}
}

func TestEffectiveRuntimeAuthUsesExplicitBinding(t *testing.T) {
	cfg := Default()
	cfg.Agents.Runtime = "pi"
	cfg.RuntimeAuth = RuntimeAuthConfig{Mode: "native", Runtime: "pi", Provider: "openai-codex"}
	binding := cfg.EffectiveRuntimeAuth()
	if binding.Mode != "native" || binding.Provider != "openai-codex" {
		t.Fatalf("binding = %#v", binding)
	}
}

func TestEffectiveRuntimeAuthCanonicalizesCredentialRef(t *testing.T) {
	cfg := Default()
	cfg.RuntimeAuth = RuntimeAuthConfig{Mode: "tack", Runtime: "pi", Provider: "openai-codex"}
	binding := cfg.EffectiveRuntimeAuth()
	if binding.CredentialRef != "openai-codex" {
		t.Fatalf("CredentialRef = %q, want openai-codex", binding.CredentialRef)
	}
}

func TestEffectiveProjectSetupFallsBackToSandboxPostCreate(t *testing.T) {
	cfg := Default()
	cfg.Sandbox.PostCreate = []string{"bun install"}
	setup := cfg.EffectiveProjectSetup()
	if len(setup.Commands) != 1 || setup.Commands[0] != "bun install" {
		t.Fatalf("setup = %#v", setup)
	}
}

func TestEffectiveProjectSetupPrefersProjectSetupCommands(t *testing.T) {
	cfg := Default()
	cfg.Sandbox.PostCreate = []string{"bun install"}
	cfg.ProjectSetup = ProjectSetupConfig{Commands: []string{"uv sync"}, Verify: []string{"test -d .venv"}}
	setup := cfg.EffectiveProjectSetup()
	if len(setup.Commands) != 1 || setup.Commands[0] != "uv sync" {
		t.Fatalf("setup commands = %#v", setup.Commands)
	}
	if len(setup.Verify) != 1 || setup.Verify[0] != "test -d .venv" {
		t.Fatalf("setup verify = %#v", setup.Verify)
	}
}

func TestLoad_DefaultGitIdentity(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Git.AuthorName != "Tack" {
		t.Fatalf("AuthorName = %q, want %q", cfg.Git.AuthorName, "Tack")
	}
	if cfg.Git.AuthorEmail != "tack@local" {
		t.Fatalf("AuthorEmail = %q, want %q", cfg.Git.AuthorEmail, "tack@local")
	}
}

func TestLayeredLoad_ProjectGitIdentityWins(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	os.WriteFile(userPath, []byte("git:\n  author_name: User Tack\n  author_email: user@example.com\n"), 0o644)
	projPath := filepath.Join(dir, "project.yaml")
	os.WriteFile(projPath, []byte("git:\n  author_name: Project Tack\n  author_email: project@example.com\n"), 0o644)

	cfg, err := Load(projPath, userPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Git.AuthorName != "Project Tack" {
		t.Fatalf("AuthorName = %q, want %q", cfg.Git.AuthorName, "Project Tack")
	}
	if cfg.Git.AuthorEmail != "project@example.com" {
		t.Fatalf("AuthorEmail = %q, want %q", cfg.Git.AuthorEmail, "project@example.com")
	}
}

func TestLayeredLoad_SlicesReplace(t *testing.T) {
	dir := t.TempDir()

	userPath := filepath.Join(dir, "user.yaml")
	os.WriteFile(userPath, []byte("quality_gates:\n  - go vet ./...\n  - go test ./...\n"), 0o644)

	projPath := filepath.Join(dir, "project.yaml")
	os.WriteFile(projPath, []byte("quality_gates:\n  - bun run lint\n  - bun run test\n"), 0o644)

	cfg, err := Load(projPath, userPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Project slice replaces entirely, not appends
	if len(cfg.QualityGates) != 2 {
		t.Fatalf("quality_gates len = %d, want 2", len(cfg.QualityGates))
	}
	if cfg.QualityGates[0] != "bun run lint" {
		t.Errorf("quality_gates[0] = %q, want 'bun run lint'", cfg.QualityGates[0])
	}
}

func TestLayeredLoad_BothMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(
		filepath.Join(dir, "nope1.yaml"),
		filepath.Join(dir, "nope2.yaml"),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should get pure defaults
	if cfg.Daemon.Listen != "127.0.0.1:9800" {
		t.Errorf("listen = %q, want default", cfg.Daemon.Listen)
	}
}

func TestLayeredLoad_EmptyPaths(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Daemon.Listen != "127.0.0.1:9800" {
		t.Errorf("listen = %q, want default", cfg.Daemon.Listen)
	}
}

func TestFindProjectRoot(t *testing.T) {
	dir := t.TempDir()

	// Create .tack/ in the root
	tackDir := filepath.Join(dir, ".tack")
	os.MkdirAll(tackDir, 0o755)
	os.WriteFile(filepath.Join(tackDir, "config.yaml"), []byte("agents:\n  runtime: pi\n"), 0o644)

	// Create a nested subdirectory
	nested := filepath.Join(dir, "src", "pkg", "deep")
	os.MkdirAll(nested, 0o755)

	// Walk-up from nested should find root
	root := FindProjectRoot(nested)
	// Resolve symlinks for macOS /var → /private/var
	wantRoot, _ := filepath.EvalSymlinks(dir)
	gotRoot, _ := filepath.EvalSymlinks(root)
	if gotRoot != wantRoot {
		t.Errorf("FindProjectRoot = %q, want %q", gotRoot, wantRoot)
	}

	// Walk-up from dir with no .tack/ should return empty
	empty := FindProjectRoot(t.TempDir())
	if empty != "" {
		t.Errorf("FindProjectRoot(no .tack) = %q, want empty", empty)
	}
}

func TestResolveProjectConfig(t *testing.T) {
	// Explicit override wins
	got, err := ResolveProjectConfig("/explicit/config.yaml")
	if err != nil {
		t.Fatalf("ResolveProjectConfig: %v", err)
	}
	if got != "/explicit/config.yaml" {
		t.Errorf("ResolveProjectConfig(override) = %q, want /explicit/config.yaml", got)
	}
}

func TestResolveProjectConfig_RejectsDeckPaths(t *testing.T) {
	for _, path := range []string{"deck.yaml", "/tmp/project/.deck/config.yaml"} {
		if _, err := ResolveProjectConfig(path); err == nil {
			t.Fatalf("ResolveProjectConfig(%q) error = nil, want rejection", path)
		}
	}
}

func TestLoad_RejectsDeckProjectConfig(t *testing.T) {
	if _, err := Load("/tmp/project/deck.yaml", ""); err == nil {
		t.Fatal("Load accepted legacy deck project config path")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("daemon:\n  listen: 0.0.0.0:1234\n"), 0o644)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Daemon.Listen != "0.0.0.0:1234" {
		t.Errorf("listen = %q, want 0.0.0.0:1234", cfg.Daemon.Listen)
	}
}
