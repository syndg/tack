package runtimeauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if probe.NativeMethods["anthropic"] != MethodOAuth || probe.NativeMethods["openai-codex"] != MethodAPIKey {
		t.Fatalf("NativeMethods = %#v", probe.NativeMethods)
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
	if probe.NativeMethods["anthropic"] != MethodOAuth {
		t.Fatalf("NativeMethods[anthropic] = %q, want oauth", probe.NativeMethods["anthropic"])
	}
}

func TestInjectEnvUsesCanonicalProviderRef(t *testing.T) {
	store, err := credentials.Load(filepath.Join(t.TempDir(), "credentials.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store.SetModelProvider("anthropic", credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: "tok-123"})
	binding := config.RuntimeAuthConfig{Mode: ModeTack, Runtime: "claude-code", Provider: "anthropic", CredentialRef: "anthropic"}
	env := map[string]string{}
	if err := InjectEnv(binding, store, nil, env); err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["ANTHROPIC_API_KEY"] != "tok-123" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want tok-123", env["ANTHROPIC_API_KEY"])
	}
	if CredentialRefForProvider("openai-codex") != "openai-codex" {
		t.Fatalf("CredentialRefForProvider(openai-codex) = %q, want openai-codex", CredentialRefForProvider("openai-codex"))
	}
}

func TestResolveCredentialBindingReportsSource(t *testing.T) {
	store, err := credentials.Load(filepath.Join(t.TempDir(), "credentials.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store.SetModelProvider("openai-codex", credentials.ProviderCredential{
		Type:         credentials.TypeOAuth,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
	})
	binding := config.RuntimeAuthConfig{Mode: ModeTack, Runtime: "pi", Provider: "openai-codex", Method: MethodOAuth}
	report, err := ResolveCredentialBinding(binding, store)
	if err != nil {
		t.Fatalf("ResolveCredentialBinding: %v", err)
	}
	if report.Provider != "openai-codex" || report.Method != MethodOAuth || report.CredentialRef != "openai-codex" || report.CredentialType != credentials.TypeOAuth || report.Source != "credentials:openai-codex" {
		t.Fatalf("report = %#v", report)
	}
}

func TestResolveCredentialBindingRejectsTypeMismatch(t *testing.T) {
	store, err := credentials.Load(filepath.Join(t.TempDir(), "credentials.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store.SetModelProvider("openai-codex", credentials.ProviderCredential{Type: credentials.TypeAPIKey, APIKey: "sk-test"})
	binding := config.RuntimeAuthConfig{Mode: ModeTack, Runtime: "pi", Provider: "openai-codex", Method: MethodOAuth}
	err = InjectEnv(binding, store, nil, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "requires oauth credential") {
		t.Fatalf("InjectEnv error = %v, want credential type mismatch", err)
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

func TestValidateBindingRejectsAnthropicOAuth(t *testing.T) {
	adapter := newPIAdapter(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\n"), nil
	})
	binding := config.RuntimeAuthConfig{Mode: ModeNative, Runtime: "pi", Provider: "anthropic", Method: MethodOAuth}
	if err := ValidateBinding(adapter, binding, "local"); err == nil {
		t.Fatal("expected anthropic oauth binding to be rejected")
	}
}
