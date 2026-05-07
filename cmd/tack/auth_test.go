package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/credentials"
)

func TestAuthAddAPIKeyNonInteractiveTriggersReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetAuthAddFlags(t)
	reloads := 0
	oldReload := notifyDaemonAuthChanged
	notifyDaemonAuthChanged = func(context.Context) error {
		reloads++
		return nil
	}
	t.Cleanup(func() { notifyDaemonAuthChanged = oldReload })
	rootCmd.SetArgs([]string{"auth", "add", "anthropic", "--api-key", "sk-ant-test"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	store, err := credentials.Load(filepath.Join(home, ".config", "tack", "credentials.yaml"))
	if err != nil {
		t.Fatalf("Load credentials: %v", err)
	}
	resolved, err := store.ModelProvider("anthropic")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if resolved.Type != credentials.TypeAPIKey || resolved.Value != "sk-ant-test" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestAuthAddOAuthNonInteractiveReportsAllMissingInputs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetAuthAddFlags(t)
	oldReload := notifyDaemonAuthChanged
	notifyDaemonAuthChanged = func(context.Context) error { return nil }
	t.Cleanup(func() { notifyDaemonAuthChanged = oldReload })
	rootCmd.SetArgs([]string{"auth", "add", "openai-codex", "--method", "oauth"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected missing oauth inputs")
	}
	for _, want := range []string{"--oauth-access-token", "--oauth-refresh-token", "--oauth-expires-at"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %s", err.Error(), want)
		}
	}
}

func resetAuthAddFlags(t *testing.T) {
	t.Helper()
	authAddMethod = ""
	authAddAPIKey = ""
	authAddOAuthAccessToken = ""
	authAddOAuthRefreshToken = ""
	authAddOAuthExpiresAt = ""
	for _, name := range []string{"method", "api-key", "oauth-access-token", "oauth-refresh-token", "oauth-expires-at"} {
		if err := authAddCmd.Flags().Set(name, ""); err != nil {
			t.Fatalf("reset flag %s: %v", name, err)
		}
	}
}
