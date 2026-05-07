package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfigSetUserTriggersDaemonReload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)
	resetConfigCommandState(t)

	reloads := 0
	oldReload := notifyDaemonConfigChanged
	notifyDaemonConfigChanged = func(context.Context) error {
		reloads++
		return nil
	}
	t.Cleanup(func() { notifyDaemonConfigChanged = oldReload })

	rootCmd.SetArgs([]string{"config", "--user", "set", "agents.runtime", "pi"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "runtime: pi") {
		t.Fatalf("config = %q, want runtime", string(raw))
	}
}

func TestConfigRemoveReportsRestartRequiredWhenReloadFails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("agents:\n  runtime: pi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("TACK_USER_CONFIG_PATH", configPath)
	resetConfigCommandState(t)

	oldReload := notifyDaemonConfigChanged
	notifyDaemonConfigChanged = func(context.Context) error { return errors.New("reload unsupported") }
	t.Cleanup(func() { notifyDaemonConfigChanged = oldReload })

	output := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"config", "--user", "remove", "agents.runtime"})
		t.Cleanup(func() { rootCmd.SetArgs(nil) })
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})
	if !strings.Contains(output, "Daemon reload failed; restart required: reload unsupported") {
		t.Fatalf("output = %q, want restart required message", output)
	}
}

func resetConfigCommandState(t *testing.T) {
	t.Helper()
	configUser = false
	configProject = false
	for _, cmd := range []*cobraCommandFlagResetter{{name: "config", cmd: configCmd}, {name: "config get", cmd: configGetCmd}, {name: "config list", cmd: configListCmd}} {
		if flag := cmd.cmd.Flags().Lookup("user"); flag != nil {
			if err := cmd.cmd.Flags().Set("user", "false"); err != nil {
				t.Fatalf("reset %s --user: %v", cmd.name, err)
			}
		}
		if flag := cmd.cmd.Flags().Lookup("project"); flag != nil {
			if err := cmd.cmd.Flags().Set("project", "false"); err != nil {
				t.Fatalf("reset %s --project: %v", cmd.name, err)
			}
		}
	}
}

type cobraCommandFlagResetter struct {
	name string
	cmd  *cobra.Command
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(out)
}
