package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Daemon       DaemonConfig   `yaml:"daemon"`
	Sandbox      SandboxConfig  `yaml:"sandbox"`
	Agents       AgentsConfig   `yaml:"agents"`
	Planning     PlanningConfig `yaml:"planning"`
	Watchdog     WatchdogConfig `yaml:"watchdog"`
	Tools        ToolsConfig    `yaml:"tools"`
	QualityGates []string       `yaml:"quality_gates"`
}

type DaemonConfig struct {
	Listen  string `yaml:"listen"`
	DataDir string `yaml:"data_dir"`
}

type SandboxConfig struct {
	Provider          string         `yaml:"provider"`
	DefaultResources  ResourceConfig `yaml:"default_resources"`
	AutoStopMinutes   int            `yaml:"auto_stop_interval"`
	AutoDeleteMinutes int            `yaml:"auto_delete_interval"`
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
	Runtime            string   `yaml:"runtime"`
	MaxConcurrent      int      `yaml:"max_concurrent"`
	MaxDepth           int      `yaml:"max_depth"`
	StaggerDelayMs     int      `yaml:"stagger_delay_ms"`
	IdleTimeoutMinutes int      `yaml:"idle_timeout_minutes"`
	Pi                 PiConfig `yaml:"pi"`
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

// Load reads a YAML config file at path and returns the parsed Config.
// If the file does not exist, it returns Default().
func Load(path string) (*Config, error) {
	// Expand ~ in the path itself before reading
	path = expandTilde(path)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return cfg, nil
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Daemon: DaemonConfig{
			Listen:  "0.0.0.0:9800",
			DataDir: "~/.deck/data",
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
		QualityGates: []string{"lint", "test", "build"},
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
