package daemonservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
	out   map[string]string
	err   map[string]error
}

func (r *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	_ = ctx
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if r.err != nil && r.err[call] != nil {
		return r.out[call], r.err[call]
	}
	if r.out == nil {
		return "", nil
	}
	return r.out[call], nil
}

func TestLaunchdInstallWritesPlistAndBootstraps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &fakeRunner{}
	provider, err := New(Options{
		GOOS:       "darwin",
		Executable: "/usr/local/bin/tack",
		ConfigPath: "/tmp/tack-config.yaml",
		LogDir:     filepath.Join(home, "logs"),
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := provider.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", LaunchdID+".plist")
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	plist := string(raw)
	for _, want := range []string{"<string>com.syndg.tack.daemon</string>", "<string>/usr/local/bin/tack</string>", "<string>daemon</string>", "TACK_USER_CONFIG_PATH", "/tmp/tack-config.yaml", "KeepAlive"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{"launchctl bootstrap gui/", "launchctl enable gui/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command %q in:\n%s", want, joined)
		}
	}
}

func TestSystemdLifecycleCommands(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	runner := &fakeRunner{}
	provider, err := New(Options{
		GOOS:       "linux",
		Executable: "/usr/bin/tack",
		ConfigPath: "/tmp/tack-user.yaml",
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := provider.Install(ctx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := provider.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := provider.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if err := provider.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	unitPath := filepath.Join(configDir, "systemd", "user", SystemdUnit)
	raw, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	unit := string(raw)
	for _, want := range []string{"Description=Tack daemon", "Environment=TACK_USER_CONFIG_PATH=/tmp/tack-user.yaml", "ExecStart=\"/usr/bin/tack\" daemon", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}

	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable tack-daemon.service",
		"systemctl --user start tack-daemon.service",
		"systemctl --user stop tack-daemon.service",
		"systemctl --user restart tack-daemon.service",
		"systemctl --user reload-or-restart tack-daemon.service",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command %q in:\n%s", want, joined)
		}
	}
}

func TestStatusUsesFakePlatformState(t *testing.T) {
	runner := &fakeRunner{out: map[string]string{
		"systemctl --user is-enabled tack-daemon.service":        "enabled",
		"systemctl --user is-active tack-daemon.service":         "active",
		"systemctl --user status tack-daemon.service --no-pager": "Loaded: loaded\nActive: active (running)",
	}}
	provider, err := New(Options{GOOS: "linux", Executable: "/usr/bin/tack", Runner: runner, Listen: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status, err := provider.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Installed || !status.Enabled || !status.Running {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Healthy {
		t.Fatalf("expected unhealthy without daemon listener")
	}
	if status.LogPath == "" || status.ConfigPath == "" {
		t.Fatalf("expected log/config paths: %+v", status)
	}
}
