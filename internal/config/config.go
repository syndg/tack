package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserConfigPath is the default location for user-level config.
const UserConfigPath = "~/.config/tack/config.yaml"

// ProjectConfigDir is the directory name Tack looks for in project roots.
const ProjectConfigDir = ".tack"

// ProjectConfigFileName is the canonical project config filename.
const ProjectConfigFileName = "config.yaml"

// ProjectIDFileName stores the stable project identity inside the repo.
const ProjectIDFileName = "project-id"

// DefaultBaseBranch is the fallback base branch when not configured.
const DefaultBaseBranch = "main"

type Config struct {
	Daemon       DaemonConfig   `yaml:"daemon"`
	Sandbox      SandboxConfig  `yaml:"sandbox"`
	Agents       AgentsConfig   `yaml:"agents"`
	Planning     PlanningConfig `yaml:"planning"`
	Watchdog     WatchdogConfig `yaml:"watchdog"`
	Tools        ToolsConfig    `yaml:"tools"`
	Git          GitConfig      `yaml:"git"`
	QualityGates []string       `yaml:"quality_gates"`
	ProjectRoot  string         `yaml:"-" json:"-"`
}

type DaemonConfig struct {
	Listen      string `yaml:"listen"`
	ExternalURL string `yaml:"external_url"` // public URL for agent callbacks (required for remote sandboxes)
	DataDir     string `yaml:"data_dir"`
	BaseBranch  string `yaml:"base_branch"`
}

type SandboxConfig struct {
	Provider          string         `yaml:"provider"`
	WorktreeDir       string         `yaml:"worktree_dir"` // override for local worktree directory (default: $TMPDIR/tack-worktrees)
	DefaultResources  ResourceConfig `yaml:"default_resources"`
	AutoStopMinutes   int            `yaml:"auto_stop_interval"`
	AutoDeleteMinutes int            `yaml:"auto_delete_interval"`
	PostCreate        []string       `yaml:"post_create"` // commands to run after worktree/sandbox creation
	Daytona           DaytonaConfig  `yaml:"daytona"`
}

type DaytonaConfig struct {
	APIKey   string `yaml:"api_key"`
	APIURL   string `yaml:"api_url"`
	Snapshot string `yaml:"snapshot"`
}

type ResourceConfig struct {
	CPU    int `yaml:"cpu"`
	Memory int `yaml:"memory"`
	Disk   int `yaml:"disk"`
}

type AgentsConfig struct {
	Runtime            string        `yaml:"runtime"`
	MaxConcurrent      int           `yaml:"max_concurrent"`
	MaxDepth           int           `yaml:"max_depth"`
	StaggerDelayMs     int           `yaml:"stagger_delay_ms"`
	IdleTimeoutMinutes int           `yaml:"idle_timeout_minutes"`
	Timeouts           TimeoutConfig `yaml:"timeouts"`
	Pi                 PiConfig      `yaml:"pi"`
}

// TimeoutConfig holds per-role timeout settings.
type TimeoutConfig struct {
	Default  RoleTimeout `yaml:"default"`
	Planner  RoleTimeout `yaml:"planner"`
	Builder  RoleTimeout `yaml:"builder"`
	Reviewer RoleTimeout `yaml:"reviewer"`
	Scout    RoleTimeout `yaml:"scout"`
}

// RoleTimeout holds timeout settings for a specific role.
type RoleTimeout struct {
	MaxDurationMinutes int `yaml:"max_duration_minutes"`
	IdleMinutes        int `yaml:"idle_minutes"`
}

// GetTimeout returns the effective timeout for a role, falling back to defaults.
func (t *TimeoutConfig) GetTimeout(role string) RoleTimeout {
	var rt RoleTimeout
	switch role {
	case "planner":
		rt = t.Planner
	case "builder":
		rt = t.Builder
	case "reviewer":
		rt = t.Reviewer
	case "scout":
		rt = t.Scout
	}
	if rt.MaxDurationMinutes == 0 {
		rt.MaxDurationMinutes = t.Default.MaxDurationMinutes
	}
	if rt.IdleMinutes == 0 {
		rt.IdleMinutes = t.Default.IdleMinutes
	}
	return rt
}

type PiConfig struct {
	Provider      string `yaml:"provider"`       // LLM provider (default: "anthropic")
	Model         string `yaml:"model"`          // model override
	ThinkingLevel string `yaml:"thinking_level"` // default: "medium"
	ExtensionPath string `yaml:"extension_path"` // custom path (default: embedded)
}

type PlanningConfig struct {
	DefaultMode string `yaml:"default_mode"`
	Model       string `yaml:"model"`
}

type WatchdogConfig struct {
	CheckIntervalSeconds int `yaml:"check_interval_seconds"`
	NudgeAfterMinutes    int `yaml:"nudge_after_minutes"`
	EscalateAfterNudges  int `yaml:"escalate_after_nudges"`
}

type ToolsConfig struct {
	MaxPerAgent   int      `yaml:"max_per_agent"`
	AlwaysInclude []string `yaml:"always_include"`
	AlwaysExclude []string `yaml:"always_exclude"`
}

type GitConfig struct {
	AuthorName  string `yaml:"author_name"`
	AuthorEmail string `yaml:"author_email"`
}

// Load reads config with two-layer merge: defaults → user config → project config.
// Either path can be empty to skip that layer.
func Load(projectPath, userPath string) (*Config, error) {
	if err := ValidateProjectConfigPath(projectPath); err != nil {
		return nil, err
	}

	cfg := Default()
	if projectPath != "" {
		cfg.ProjectRoot = filepath.Dir(expandTilde(projectPath))
	}

	// Layer 1: user config (fallback)
	if err := mergeFromFile(cfg, userPath); err != nil {
		return nil, fmt.Errorf("loading user config: %w", err)
	}

	// Layer 2: project config (wins)
	if err := mergeFromFile(cfg, projectPath); err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	return cfg, nil
}

// LoadFile reads a single YAML config file and merges it over defaults.
// This is the legacy single-file loader — prefer Load() for layered config.
func LoadFile(path string) (*Config, error) {
	if err := ValidateProjectConfigPath(path); err != nil {
		return nil, err
	}
	cfg := Default()
	if err := mergeFromFile(cfg, path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// mergeFromFile reads a YAML file and deep-merges it into cfg.
// Missing files are silently skipped.
func mergeFromFile(cfg *Config, path string) error {
	if path == "" {
		return nil
	}
	path = expandTilde(path)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// Unmarshal into cfg directly — yaml.v3 leaves unset fields unchanged,
	// giving us the scalar-replace, map-merge behavior we want.
	// Slices are replaced entirely (not appended), which matches design spec.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// FindProjectRoot walks up from startDir looking for a .tack/ directory.
// Returns the directory containing .tack/, or empty string if not found.
func FindProjectRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, ProjectConfigDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

// IsLegacyProjectConfigPath reports whether path points at a Deck-era project config.
func IsLegacyProjectConfigPath(path string) bool {
	if path == "" {
		return false
	}
	path = filepath.ToSlash(filepath.Clean(expandTilde(path)))
	base := filepath.Base(path)
	if base == "deck.yaml" || base == "deck.yml" {
		return true
	}
	return strings.HasSuffix(path, "/.deck/config.yaml") || strings.HasSuffix(path, "/.deck/config.yml")
}

// ValidateProjectConfigPath rejects Deck-era project config paths.
func ValidateProjectConfigPath(path string) error {
	if !IsLegacyProjectConfigPath(path) {
		return nil
	}
	return fmt.Errorf("legacy Deck project config path %q is no longer supported; move it to .tack/config.yaml", path)
}

// ResolveProjectConfig finds the project config path by either using the
// explicit override or walking up from cwd to find .tack/config.yaml.
func ResolveProjectConfig(override string) (string, error) {
	if override != "" {
		if err := ValidateProjectConfigPath(override); err != nil {
			return "", err
		}
		return override, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil
	}
	root := FindProjectRoot(cwd)
	if root == "" {
		return "", nil
	}
	return ProjectConfigPath(root), nil
}

// ProjectConfigPath returns the canonical config path for a repo root.
func ProjectConfigPath(root string) string {
	return filepath.Join(root, ProjectConfigDir, ProjectConfigFileName)
}

// ProjectIDPath returns the canonical project ID file path for a repo root.
func ProjectIDPath(root string) string {
	return filepath.Join(root, ProjectConfigDir, ProjectIDFileName)
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Daemon: DaemonConfig{
			Listen:     "0.0.0.0:9800",
			DataDir:    "~/.config/tack/data",
			BaseBranch: "main",
		},
		Sandbox: SandboxConfig{
			Provider: "daytona",
			DefaultResources: ResourceConfig{
				CPU:    2,
				Memory: 2048,
				Disk:   10,
			},
			AutoStopMinutes:   30,
			AutoDeleteMinutes: 1440,
		},
		Agents: AgentsConfig{
			Runtime:            "claude-code",
			MaxConcurrent:      8,
			MaxDepth:           2,
			StaggerDelayMs:     500,
			IdleTimeoutMinutes: 15,
			Timeouts: TimeoutConfig{
				Default: RoleTimeout{MaxDurationMinutes: 30, IdleMinutes: 10},
			},
		},
		Planning: PlanningConfig{
			DefaultMode: "collaborative",
			Model:       "claude-sonnet-4-20250514",
		},
		Watchdog: WatchdogConfig{
			CheckIntervalSeconds: 30,
			NudgeAfterMinutes:    10,
			EscalateAfterNudges:  3,
		},
		Tools: ToolsConfig{
			MaxPerAgent:   20,
			AlwaysInclude: []string{},
			AlwaysExclude: []string{},
		},
		Git: GitConfig{
			AuthorName:  "Tack",
			AuthorEmail: "tack@local",
		},
		QualityGates: []string{"go vet ./...", "go test ./...", "go build ./..."},
	}
}

// ExpandPaths expands ~ in DataDir to the actual home directory.
func (c *Config) ExpandPaths() {
	c.Daemon.DataDir = expandTilde(c.Daemon.DataDir)
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
