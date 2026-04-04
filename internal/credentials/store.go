package credentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/syndg/tack/internal/providerauth"
	"gopkg.in/yaml.v3"
)

// Credential types.
const (
	TypeAPIKey = "api_key"
	TypePAT    = "pat"
	TypeOAuth  = "oauth"
)

// envVarPattern matches strings that look like environment variable names.
var envVarPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// shellTimeout is the maximum time for shell command value resolution.
const shellTimeout = 10 * time.Second

// Credentials is the top-level YAML structure of ~/.config/tack/credentials.yaml.
type Credentials struct {
	Providers            map[string]ProviderCredential `yaml:"providers,omitempty"`
	LegacyModelProviders map[string]ProviderCredential `yaml:"model_providers,omitempty"`
	Git                  *GitCredential                `yaml:"git,omitempty"`
	Sandbox              map[string]SandboxCredential  `yaml:"sandbox,omitempty"`
}

// ProviderCredential holds authentication for a model provider (Anthropic, OpenAI, etc.).
type ProviderCredential struct {
	Type         string `yaml:"type" json:"type"`
	APIKey       string `yaml:"api_key,omitempty" json:"key,omitempty"`
	AccessToken  string `yaml:"access_token,omitempty" json:"access,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty" json:"refresh,omitempty"`
	ExpiresAt    int64  `yaml:"expires_at,omitempty" json:"expires,omitempty"`
	AccountID    string `yaml:"account_id,omitempty" json:"accountId,omitempty"`
}

// GitCredential holds a personal access token for git operations.
type GitCredential struct {
	Type  string `yaml:"type"`
	Host  string `yaml:"host"`
	Token string `yaml:"token"`
}

// SandboxCredential holds API keys for sandbox providers (e.g., Daytona).
type SandboxCredential struct {
	Type   string `yaml:"type"`
	APIKey string `yaml:"api_key,omitempty"`
}

// ResolvedProvider is the result of looking up and resolving a model provider credential.
type ResolvedProvider struct {
	Type  string // "api_key" or "oauth"
	Value string // the resolved secret value
}

// Store manages credentials from a YAML file with value resolution.
type Store struct {
	path  string
	data  Credentials
	mu    sync.RWMutex
	cache map[string]string // shell command result cache
}

// Load reads credentials from the given path. Returns an empty store if the file doesn't exist.
func Load(path string) (*Store, error) {
	path = expandTilde(path)

	s := &Store{
		path:  path,
		cache: make(map[string]string),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = Credentials{
				Providers: make(map[string]ProviderCredential),
				Sandbox:   make(map[string]SandboxCredential),
			}
			return s, nil
		}
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	if err := yaml.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	if s.data.Providers == nil {
		s.data.Providers = make(map[string]ProviderCredential)
	}
	if len(s.data.Providers) == 0 && len(s.data.LegacyModelProviders) > 0 {
		s.data.Providers = s.data.LegacyModelProviders
	}
	if s.data.Sandbox == nil {
		s.data.Sandbox = make(map[string]SandboxCredential)
	}
	return s, nil
}

// Save writes the credentials to disk with 0600 permissions.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := yaml.Marshal(&s.data)
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating credentials directory: %w", err)
	}
	return os.WriteFile(s.path, raw, 0o600)
}

// Path returns the file path of the credentials store.
func (s *Store) Path() string { return s.path }

// --- Model Provider ---

// ModelProvider resolves the credential for a named provider.
func (s *Store) ModelProvider(name string) (*ResolvedProvider, error) {
	cred, err := s.GetProviderCredential(name)
	if err != nil {
		return nil, err
	}
	switch cred.Type {
	case TypeAPIKey:
		return &ResolvedProvider{Type: cred.Type, Value: cred.APIKey}, nil
	case TypeOAuth:
		return &ResolvedProvider{Type: cred.Type, Value: cred.AccessToken}, nil
	default:
		return nil, fmt.Errorf("unknown credential type %q for provider %q", cred.Type, name)
	}
}

// GetProviderCredential returns a provider credential with any shell/env values resolved and OAuth refreshed if needed.
func (s *Store) GetProviderCredential(name string) (*ProviderCredential, error) {
	s.mu.RLock()
	cred, ok := s.data.Providers[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no credential for provider %q", name)
	}
	resolved := cred
	switch resolved.Type {
	case TypeAPIKey:
		val, err := s.resolve(resolved.APIKey)
		if err != nil {
			return nil, fmt.Errorf("resolving %s credential: %w", name, err)
		}
		resolved.APIKey = val
		return &resolved, nil
	case TypeOAuth:
		if name != "openai-codex" {
			return nil, fmt.Errorf("oauth credentials for provider %q are not supported", name)
		}
		if time.Now().Add(2*time.Minute).UnixMilli() >= resolved.ExpiresAt {
			refreshed, err := providerauth.RefreshOpenAICodexToken(context.Background(), resolved.RefreshToken)
			if err != nil {
				return nil, fmt.Errorf("refreshing %s oauth: %w", name, err)
			}
			resolved.AccessToken = refreshed.AccessToken
			resolved.RefreshToken = refreshed.RefreshToken
			resolved.ExpiresAt = refreshed.ExpiresAt
			resolved.AccountID = refreshed.AccountID
			s.SetModelProvider(name, resolved)
			if err := s.Save(); err != nil {
				return nil, err
			}
		}
		return &resolved, nil
	default:
		return nil, fmt.Errorf("unknown credential type %q for provider %q", resolved.Type, name)
	}
}

// SetModelProvider adds or replaces a provider credential.
func (s *Store) SetModelProvider(name string, cred ProviderCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Providers[name] = cred
}

// RemoveModelProvider removes a provider credential.
func (s *Store) RemoveModelProvider(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Providers, name)
}

// --- Git ---

// GitToken resolves the git PAT for the given host. If host is empty, uses the default.
func (s *Store) GitToken(host string) (string, error) {
	s.mu.RLock()
	cred := s.data.Git
	s.mu.RUnlock()
	if cred == nil {
		return "", fmt.Errorf("no git credential configured")
	}
	if host != "" && cred.Host != host {
		return "", fmt.Errorf("no git credential for host %q (have %q)", host, cred.Host)
	}
	return s.resolve(cred.Token)
}

// SetGit adds or replaces the git credential.
func (s *Store) SetGit(cred GitCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Git = &cred
}

// RemoveGit removes the git credential.
func (s *Store) RemoveGit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Git = nil
}

// GitHost returns the configured git host, or empty string if not set.
func (s *Store) GitHost() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.Git == nil {
		return ""
	}
	return s.data.Git.Host
}

// --- Sandbox ---

// SandboxKey resolves the API key for a sandbox provider.
func (s *Store) SandboxKey(name string) (string, error) {
	s.mu.RLock()
	cred, ok := s.data.Sandbox[name]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no credential for sandbox provider %q", name)
	}
	return s.resolve(cred.APIKey)
}

// SetSandbox adds or replaces a sandbox credential.
func (s *Store) SetSandbox(name string, cred SandboxCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sandbox[name] = cred
}

// RemoveSandbox removes a sandbox credential.
func (s *Store) RemoveSandbox(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sandbox, name)
}

// --- Listing ---

// ListProviders returns the names and types of all configured providers.
func (s *Store) ListProviders() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data.Providers))
	for name, cred := range s.data.Providers {
		out[name] = cred.Type
	}
	return out
}

// HasProvider checks if a provider credential exists.
func (s *Store) HasProvider(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data.Providers[name]
	return ok
}

// HasGit checks if a git credential is configured.
func (s *Store) HasGit() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Git != nil
}

// HasSandbox checks if a sandbox credential exists.
func (s *Store) HasSandbox(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data.Sandbox[name]
	return ok
}

// --- Value Resolution ---

// resolve interprets a raw credential value:
//   - "!cmd args" → shell command (cached)
//   - "ALL_CAPS" → environment variable
//   - otherwise → literal
func (s *Store) resolve(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty credential value")
	}

	// Shell command
	if strings.HasPrefix(raw, "!") {
		return s.resolveShell(raw[1:])
	}

	// Environment variable
	if envVarPattern.MatchString(raw) {
		val := os.Getenv(raw)
		if val == "" {
			return "", fmt.Errorf("environment variable %s is not set", raw)
		}
		return val, nil
	}

	// Literal
	return raw, nil
}

func (s *Store) resolveShell(cmd string) (string, error) {
	s.mu.RLock()
	cached, ok := s.cache[cmd]
	s.mu.RUnlock()
	if ok {
		return cached, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
	if err != nil {
		return "", fmt.Errorf("shell command %q failed: %w", cmd, err)
	}

	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", fmt.Errorf("shell command %q returned empty output", cmd)
	}

	s.mu.Lock()
	s.cache[cmd] = val
	s.mu.Unlock()
	return val, nil
}

// --- Validation ---

// ValidateSetupToken checks that a setup token has the expected prefix and minimum length.
func ValidateSetupToken(token string) error {
	if !strings.HasPrefix(token, "sk-ant-oat01-") {
		return fmt.Errorf("setup token must start with sk-ant-oat01- prefix")
	}
	if len(token) < 80 {
		return fmt.Errorf("setup token too short (%d chars, minimum 80)", len(token))
	}
	return nil
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
