package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.data.ModelProviders) != 0 {
		t.Fatal("expected empty model_providers")
	}
	if s.data.Git != nil {
		t.Fatal("expected nil git")
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	s.SetModelProvider("anthropic", ProviderCredential{Type: TypeAPIKey, APIKey: "sk-ant-test"})
	s.SetModelProvider("openai", ProviderCredential{Type: TypeAPIKey, APIKey: "sk-test"})
	s.SetGit(GitCredential{Type: TypePAT, Host: "github.com", Token: "ghp_test"})
	s.SetSandbox("daytona", SandboxCredential{Type: TypeAPIKey, APIKey: "dtn_test"})

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}

	// Reload
	s2, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}

	// Model provider
	rp, err := s2.ModelProvider("anthropic")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if rp.Type != TypeAPIKey || rp.Value != "sk-ant-test" {
		t.Errorf("anthropic = %+v, want api_key/sk-ant-test", rp)
	}

	// Git
	tok, err := s2.GitToken("")
	if err != nil {
		t.Fatalf("GitToken: %v", err)
	}
	if tok != "ghp_test" {
		t.Errorf("git token = %q, want ghp_test", tok)
	}

	// Sandbox
	key, err := s2.SandboxKey("daytona")
	if err != nil {
		t.Fatalf("SandboxKey: %v", err)
	}
	if key != "dtn_test" {
		t.Errorf("sandbox key = %q, want dtn_test", key)
	}
}

func TestResolveEnvVar(t *testing.T) {
	t.Setenv("DECK_TEST_SECRET", "resolved-from-env")
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("test", ProviderCredential{Type: TypeAPIKey, APIKey: "DECK_TEST_SECRET"})

	rp, err := s.ModelProvider("test")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if rp.Value != "resolved-from-env" {
		t.Errorf("resolved = %q, want resolved-from-env", rp.Value)
	}
}

func TestResolveEnvVarUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("test", ProviderCredential{Type: TypeAPIKey, APIKey: "NONEXISTENT_VAR_XYZ"})

	_, err := s.ModelProvider("test")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestResolveShellCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("test", ProviderCredential{Type: TypeAPIKey, APIKey: "!echo shell-secret"})

	rp, err := s.ModelProvider("test")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if rp.Value != "shell-secret" {
		t.Errorf("resolved = %q, want shell-secret", rp.Value)
	}

	// Second call should use cache
	rp2, err := s.ModelProvider("test")
	if err != nil {
		t.Fatalf("ModelProvider (cached): %v", err)
	}
	if rp2.Value != "shell-secret" {
		t.Errorf("cached = %q, want shell-secret", rp2.Value)
	}
}

func TestResolveLiteral(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("test", ProviderCredential{Type: TypeAPIKey, APIKey: "sk-ant-api03-literal"})

	rp, err := s.ModelProvider("test")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if rp.Value != "sk-ant-api03-literal" {
		t.Errorf("resolved = %q, want sk-ant-api03-literal", rp.Value)
	}
}

func TestSetupTokenValidation(t *testing.T) {
	tests := []struct {
		token string
		ok    bool
	}{
		{"sk-ant-oat01-abc", false}, // right prefix but too short (17 chars)
		{"sk-ant-oat01-abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01234", true},  // exactly 80
		{"sk-ant-oat01-abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz012345", true}, // 81
		{"sk-ant-api03-short", false}, // wrong prefix
		{"sk-ant-oat01-short", false}, // too short
	}
	for _, tt := range tests {
		err := ValidateSetupToken(tt.token)
		if tt.ok && err != nil {
			t.Errorf("ValidateSetupToken(%q[:20]...) = %v, want nil", tt.token[:20], err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateSetupToken(%q[:20]...) = nil, want error", tt.token[:20])
		}
	}
}

func TestSetupTokenType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("anthropic", ProviderCredential{
		Type:  TypeSetupToken,
		Token: "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01234",
	})

	rp, err := s.ModelProvider("anthropic")
	if err != nil {
		t.Fatalf("ModelProvider: %v", err)
	}
	if rp.Type != TypeSetupToken {
		t.Errorf("type = %q, want setup_token", rp.Type)
	}
}

func TestOAuthUnsupported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("test", ProviderCredential{Type: TypeOAuth, AccessToken: "tok"})

	_, err := s.ModelProvider("test")
	if err == nil {
		t.Fatal("expected error for oauth type")
	}
}

func TestGitHostMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetGit(GitCredential{Type: TypePAT, Host: "github.com", Token: "ghp_test"})

	_, err := s.GitToken("gitlab.com")
	if err == nil {
		t.Fatal("expected error for host mismatch")
	}
}

func TestRemoveOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)

	s.SetModelProvider("anthropic", ProviderCredential{Type: TypeAPIKey, APIKey: "test"})
	s.SetGit(GitCredential{Type: TypePAT, Host: "github.com", Token: "test"})
	s.SetSandbox("daytona", SandboxCredential{Type: TypeAPIKey, APIKey: "test"})

	if !s.HasProvider("anthropic") {
		t.Fatal("expected HasProvider true")
	}
	s.RemoveModelProvider("anthropic")
	if s.HasProvider("anthropic") {
		t.Fatal("expected HasProvider false after remove")
	}

	if !s.HasGit() {
		t.Fatal("expected HasGit true")
	}
	s.RemoveGit()
	if s.HasGit() {
		t.Fatal("expected HasGit false after remove")
	}

	if !s.HasSandbox("daytona") {
		t.Fatal("expected HasSandbox true")
	}
	s.RemoveSandbox("daytona")
	if s.HasSandbox("daytona") {
		t.Fatal("expected HasSandbox false after remove")
	}
}

func TestListProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.yaml")
	s, _ := Load(path)
	s.SetModelProvider("anthropic", ProviderCredential{Type: TypeAPIKey, APIKey: "test"})
	s.SetModelProvider("openai", ProviderCredential{Type: TypeAPIKey, APIKey: "test"})

	providers := s.ListProviders()
	if len(providers) != 2 {
		t.Fatalf("ListProviders = %d entries, want 2", len(providers))
	}
	if providers["anthropic"] != TypeAPIKey {
		t.Errorf("anthropic type = %q, want api_key", providers["anthropic"])
	}
}
