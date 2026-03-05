package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Daemon.Listen != "0.0.0.0:9800" {
		t.Fatalf("unexpected default listen: %q", cfg.Daemon.Listen)
	}
}

func TestLoadMergesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("daemon:\n  listen: 127.0.0.1:9999\nplanning:\n  model: custom-model\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
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
	cfg.Daemon.DataDir = "~/.deck/test-data"
	cfg.ExpandPaths()

	want := filepath.Join(home, ".deck/test-data")
	if cfg.Daemon.DataDir != want {
		t.Fatalf("expected %q, got %q", want, cfg.Daemon.DataDir)
	}
}
