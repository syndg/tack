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

	"github.com/syndg/tack/internal/daemonservice"
	"github.com/syndg/tack/internal/runtimecatalog"
	"github.com/syndg/tack/internal/validation"
)

type fakeDoctorRuntimeRunner struct {
	paths map[string]string
	out   map[string][]byte
}

type fakeDoctorDaemonService struct {
	status daemonservice.Status
	err    error
}

func (f fakeDoctorDaemonService) Install(context.Context) error { return nil }
func (f fakeDoctorDaemonService) Start(context.Context) error   { return nil }
func (f fakeDoctorDaemonService) Stop(context.Context) error    { return nil }
func (f fakeDoctorDaemonService) Restart(context.Context) error { return nil }
func (f fakeDoctorDaemonService) Reload(context.Context) error  { return nil }
func (f fakeDoctorDaemonService) Status(context.Context) (daemonservice.Status, error) {
	return f.status, f.err
}

func setDoctorDaemonService(t *testing.T, status daemonservice.Status, err error) {
	t.Helper()
	doctorDaemonServiceProvider = func() (daemonservice.Provider, error) {
		return fakeDoctorDaemonService{status: status, err: err}, nil
	}
	t.Cleanup(func() { doctorDaemonServiceProvider = newDaemonServiceProvider })
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
	if err := os.WriteFile(projectConfig, []byte("daemon:\n  listen: 127.0.0.1:9900\nmodels:\n  agent: project-agent\nquality_gates:\n  - bun test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile project config: %v", err)
	}
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n  service: service\ndaemon:\n  data_dir: /tmp/tack-data\nagents:\n  runtime: pi\nruntime_auth:\n  provider: anthropic\n  mode: native\n  method: api_key\n  credential_ref: anthropic-main\nmodels:\n  agent: global-agent\n  planner: global-planner\n  small_tasks: global-small\nsandbox:\n  provider: local\nblueprint: standard\nquality_gates:\n  - go test ./...\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--config", projectConfig, "doctor"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"pi": "/tmp/pi"}, out: map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic project-agent 200K 32K yes yes\nanthropic global-planner 200K 32K yes yes\nanthropic global-small 200K 32K yes yes\n")}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return true, "daemon can see Pi" }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "launchd", Installed: true, Enabled: true, Running: true, Healthy: true, Listen: "127.0.0.1:9900", ConfigPath: userConfig, LogPath: "/tmp/tack.log"}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"[pass] project_config", "[pass] user_config", "[pass] global_setup", "setup.service=service", "agents.runtime=pi", "source: global", "models.agent=project-agent", "source: project_override", "quality_gates=bun test", "daemon.listen=127.0.0.1:9900", "[pass] daemon_service", "installed=true", "logs=/tmp/tack.log", "[pass] pi_runtime", "[pass] pi_model_catalog"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n  service: service\ndaemon:\n  listen: 127.0.0.1:9901\nagents:\n  runtime: pi\nruntime_auth:\n  provider: anthropic\n  mode: native\n  method: api_key\n  credential_ref: anthropic-main\nmodels:\n  agent: global-agent\n  planner: global-planner\n  small_tasks: global-small\nsandbox:\n  provider: local\nblueprint: build-review\nquality_gates:\n  - go test ./...\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"doctor", "--json"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"npm": "/usr/bin/npm"}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return false, "daemon not running" }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "systemd", Installed: true, Enabled: false, Running: false, Healthy: false, ConfigPath: userConfig, LogPath: "/tmp/tack.log"}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Findings) != 21 {
		t.Fatalf("findings = %d, want 21", len(report.Findings))
	}
	if report.Findings[0].Check != "project_config" || report.Findings[0].Status == "" {
		t.Fatalf("first finding = %#v", report.Findings[0])
	}
	if report.Findings[3].Check != "effective_config" || report.Findings[3].Source != "global" {
		t.Fatalf("effective_config finding = %#v", report.Findings[3])
	}
	if report.Findings[15].Check != "daemon_service" || report.Findings[15].Status != validation.StatusWarn {
		t.Fatalf("daemon_service finding = %#v", report.Findings[15])
	}
	if report.Findings[16].Check != "runtime_auth" || report.Findings[16].Status == "" {
		t.Fatalf("runtime_auth finding = %#v", report.Findings[16])
	}
	if report.Findings[17].Check != "blueprint_preflight" || report.Findings[17].Status == "" {
		t.Fatalf("blueprint_preflight finding = %#v", report.Findings[17])
	}
	if report.Findings[18].Check != "pi_runtime" || report.Findings[18].Status != validation.StatusFail {
		t.Fatalf("pi_runtime finding = %#v", report.Findings[18])
	}
	if report.Findings[19].Check != "pi_model_catalog" || report.Findings[19].Status != validation.StatusSkip {
		t.Fatalf("pi_model_catalog finding = %#v", report.Findings[19])
	}
}

func TestDoctorFailsPiModelOutsideCatalog(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n  service: service\ndaemon:\n  listen: 127.0.0.1:9901\nagents:\n  runtime: pi\nruntime_auth:\n  provider: anthropic\n  mode: native\n  method: api_key\n  credential_ref: anthropic-main\nmodels:\n  agent: missing-agent\n  planner: claude-opus-4-1\n  small_tasks: claude-haiku\nsandbox:\n  provider: local\nblueprint: custom-no-pr\nquality_gates:\n  - go test ./...\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"doctor", "--json"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"pi": "/tmp/pi"}, out: map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\nanthropic claude-haiku 200K 32K yes yes\n")}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return true, "daemon can see Pi" }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "launchd", Installed: true, Enabled: true, Running: true, Healthy: true, ConfigPath: userConfig}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	var found validation.Finding
	for _, finding := range report.Findings {
		if finding.Check == "pi_model_catalog" {
			found = finding
			break
		}
	}
	if found.Status != validation.StatusFail || !strings.Contains(found.Evidence, "models.agent=missing-agent") || !strings.Contains(found.Fix, "Pi model catalog") {
		t.Fatalf("pi_model_catalog finding = %#v", found)
	}
}

func TestDoctorChecksBlueprintPreflightRequirements(t *testing.T) {
	root := t.TempDir()
	projectConfig := filepath.Join(root, ".tack", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(projectConfig, []byte("blueprint: standard\n"), 0o644); err != nil {
		t.Fatalf("WriteFile project config: %v", err)
	}
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n  service: service\ndaemon:\n  listen: 127.0.0.1:9901\nagents:\n  runtime: pi\nruntime_auth:\n  provider: anthropic\n  mode: native\n  method: api_key\n  credential_ref: anthropic-main\nmodels:\n  agent: global-agent\n  planner: global-planner\n  small_tasks: global-small\nsandbox:\n  provider: local\nquality_gates:\n  - go test ./...\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--config", projectConfig, "doctor", "--json"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"pi": "/tmp/pi"}, out: map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\n")}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return true, "daemon can see Pi" }
	doctorGitRemote = func(context.Context, string) (string, error) { return "git@github.com:owner/repo.git", nil }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "launchd", Installed: true, Enabled: true, Running: true, Healthy: true, ConfigPath: userConfig}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	var found validation.Finding
	for _, finding := range report.Findings {
		if finding.Check == "preflight.git_auth" {
			found = finding
			break
		}
	}
	if found.Status != validation.StatusFail || found.Source != "blueprint_requirement" || !strings.Contains(found.Summary, "create_pr requires stored git credentials") || found.Details["blueprint_id"] != "standard" {
		t.Fatalf("blueprint preflight finding = %#v", found)
	}
	if !report.Failed {
		t.Fatalf("report.Failed = false, want true")
	}
}

func TestDoctorFailsMissingEffectiveConfig(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n  service: service\ndaemon:\n  listen: 127.0.0.1:9901\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"doctor", "--json"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"npm": "/usr/bin/npm"}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return false, "daemon not running" }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "systemd", Installed: false}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	failures := 0
	for _, finding := range report.Findings {
		if finding.Check == "effective_config" && finding.Status == validation.StatusFail {
			failures++
		}
	}
	if failures == 0 || !report.Failed {
		t.Fatalf("effective_config failures = %d report.Failed=%v", failures, report.Failed)
	}
}

func TestDoctorFailsIncompleteGlobalSetup(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  phases:\n    daemon_service:\n      complete: true\ndaemon:\n  listen: 127.0.0.1:9901\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"doctor", "--json"})
	doctorRuntimeRunner = fakeDoctorRuntimeRunner{paths: map[string]string{"npm": "/usr/bin/npm"}}
	doctorDaemonCanSeePi = func(context.Context, string) (bool, string) { return false, "daemon not running" }
	setDoctorDaemonService(t, daemonservice.Status{Provider: "systemd", Installed: false}, nil)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		doctorJSON = false
		cfgPath = ""
		doctorRuntimeRunner = runtimecatalog.ExecRunner{}
		doctorDaemonCanSeePi = runtimecatalog.DaemonCanSeePi
		doctorGitRemote = nil
		doctorDaemonServiceProvider = newDaemonServiceProvider
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor --json: %v\nstderr: %s", err, stderr.String())
	}
	var report validation.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal doctor JSON: %v\n%s", err, stdout.String())
	}
	var found validation.Finding
	for _, finding := range report.Findings {
		if finding.Check == "global_setup" {
			found = finding
			break
		}
	}
	if found.Status != validation.StatusFail || !strings.Contains(found.Evidence, "next_phase=runtime") || found.Fix == "" {
		t.Fatalf("global_setup finding = %#v", found)
	}
	if !report.Failed {
		t.Fatalf("report.Failed = false, want true")
	}
}

func TestDoctorFailsCompleteGlobalSetupMissingService(t *testing.T) {
	userConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(userConfig, []byte("setup:\n  complete: true\n"), 0o644); err != nil {
		t.Fatalf("WriteFile user config: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", userConfig)

	findings, err := checkGlobalSetup(context.Background())
	if err != nil {
		t.Fatalf("checkGlobalSetup: %v", err)
	}
	if len(findings) != 1 || findings[0].Status != validation.StatusFail || !strings.Contains(findings[0].Evidence, "setup.service is not set") {
		t.Fatalf("global_setup findings = %#v", findings)
	}
}
