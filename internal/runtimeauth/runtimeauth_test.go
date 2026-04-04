package runtimeauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
)

func TestPIProbeUsesNativeProvidersAndLiveCatalog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"anthropic":{"type":"oauth"},"openai-codex":{"type":"api_key"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	adapter := newPIAdapter(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\nopenai-codex gpt-5.4 272K 128K yes yes\n"), nil
	})
	probe, err := adapter.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !probe.NativeAvailable {
		t.Fatal("expected native providers to be detected")
	}
	if len(probe.NativeProviders) != 2 {
		t.Fatalf("NativeProviders = %v, want 2 providers", probe.NativeProviders)
	}
	if err := adapter.ValidateModel("openai-codex", "gpt-5.4"); err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
	if got := adapter.Models("anthropic")[0].Label; got == adapter.Models("anthropic")[0].ID {
		t.Fatalf("expected rich model label, got %q", got)
	}
}

func TestClaudeProbeParsesAuthStatus(t *testing.T) {
	adapter := newClaudeAdapter(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"test@example.com"}`), nil
	})
	probe, err := adapter.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !probe.NativeAvailable {
		t.Fatal("expected Claude native auth to be detected")
	}
}

func TestInjectEnvUsesCanonicalProviderRefAndOAuthVar(t *testing.T) {
	store, err := credentials.Load(filepath.Join(t.TempDir(), "credentials.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store.SetModelProvider("anthropic", credentials.ProviderCredential{Type: credentials.TypeSetupToken, Token: "tok-123"})
	binding := config.RuntimeAuthConfig{Mode: ModeTack, Runtime: "claude-code", Provider: "anthropic", CredentialRef: "anthropic"}
	env := map[string]string{}
	if err := InjectEnv(binding, store, nil, env); err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["ANTHROPIC_OAUTH_TOKEN"] != "tok-123" {
		t.Fatalf("ANTHROPIC_OAUTH_TOKEN = %q, want tok-123", env["ANTHROPIC_OAUTH_TOKEN"])
	}
	if CredentialRefForProvider("openai-codex") != "openai" {
		t.Fatalf("CredentialRefForProvider(openai-codex) = %q, want openai", CredentialRefForProvider("openai-codex"))
	}
}

func TestValidateBindingRejectsNativeRemote(t *testing.T) {
	adapter := newClaudeAdapter(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"loggedIn":true}`), nil
	})
	binding := config.RuntimeAuthConfig{Mode: ModeNative, Runtime: "claude-code", Provider: "anthropic"}
	if err := ValidateBinding(adapter, binding, "daytona"); err == nil {
		t.Fatal("expected native remote binding to be rejected")
	}
}
