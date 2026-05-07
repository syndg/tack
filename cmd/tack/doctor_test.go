package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/runtimecatalog"
	"github.com/syndg/tack/internal/validation"
)

type fakeDoctorRuntimeRunner struct {
	paths map[string]string
	out   map[string][]byte
}

func (f fakeDoctorRuntimeRunner) LookPath(name string) (string, error) {
	if path := f.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (f fakeDoctorRuntimeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	return f.out[key], nil
}

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
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"pi": "/tmp/pi"}, out: map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\n")}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return true, "daemon can see Pi" }
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"[pass] project_config", "[pass] user_config", "daemon.listen=127.0.0.1:9900", "[pass] pi_runtime"} {
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
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"npm": "/usr/bin/npm"}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return false, "daemon not running" }
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Findings) != 5 {
		t.Fatalf("findings = %d, want 5", len(report.Findings))
	}
	if report.Findings[0].Check != "project_config" || report.Findings[0].Status == "" {
		t.Fatalf("first finding = %#v", report.Findings[0])
	}
	if report.Findings[3].Check != "pi_runtime" || report.Findings[3].Status != validation.StatusFail {
		t.Fatalf("pi_runtime finding = %#v", report.Findings[3])
	}
}
