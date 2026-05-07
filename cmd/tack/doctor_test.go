package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/validation"
)

func TestDoctorHumanOutput(t *testing.T) {
	root := t.TempDir()
	projectConfig := filepath.Join(root, ".tack", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(projectConfig, []byte("daemon:\n  listen: 127.0.0.1:9900\n"), 0o644); err != nil {
		t.Fatalf("WriteFile project config: %v", err)
	}
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("daemon:\n  data_dir: /tmp/tack-data\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--config", projectConfig, "doctor"})
	t.Cleanup(func() { rootCmd.SetArgs(nil); doctorJSON = false; cfgPath = "" })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"[pass] project_config", "[pass] user_config", "daemon.listen=127.0.0.1:9900"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("daemon:\n  listen: 127.0.0.1:9901\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"doctor", "--json"})
	t.Cleanup(func() { rootCmd.SetArgs(nil); doctorJSON = false; cfgPath = "" })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(report.Findings))
	}
	if report.Findings[0].Check != "project_config" || report.Findings[0].Status == "" {
		t.Fatalf("first finding = %#v", report.Findings[0])
	}
}
