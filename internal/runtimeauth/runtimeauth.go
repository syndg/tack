package runtimeauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"gopkg.in/yaml.v3"
)

const (
	ModeNative = "native"
	ModeTack   = "tack"
)

type ModelOption struct {
	ID    string
	Label string
}

type ProviderOption struct {
	ID    string
	Label string
}

type ProbeResult struct {
	Runtime            string
	RuntimeAvailable   bool
	NativeAvailable    bool
	NativeProviders    []string
	NativeDescription  string
	SuggestedProvider  string
	SupportedProviders []ProviderOption
}

type Adapter interface {
	Runtime() string
	Probe(ctx context.Context) (ProbeResult, error)
	Models(provider string) []ModelOption
	ValidateProvider(provider string) error
	ValidateModel(provider, model string) error
	SupportsNativeLocal() bool
	SupportsNativeRemote() bool
}

type execFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

type piAuthFile struct {
	Providers map[string]piAuthCredential
}

type piAuthCredential struct {
	Type string `yaml:"type"`
}

type claudeAuthStatus struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	Email        string `json:"email"`
	ApiProvider  string `json:"apiProvider"`
	Subscription string `json:"subscriptionType"`
}

type adapter struct {
	runtime    string
	providers  []ProviderOption
	models     map[string][]ModelOption
	probe      func(ctx context.Context) (ProbeResult, error)
	localOnly  bool
	remoteOkay bool
}

var providerCredentialRefs = map[string]string{
	"anthropic":    "anthropic",
	"openai":       "openai",
	"openai-codex": "openai",
	"gemini":       "gemini",
	"google":       "gemini",
	"groq":         "groq",
	"mistral":      "mistral",
	"xai":          "xai",
}

func (a *adapter) Runtime() string { return a.runtime }

func (a *adapter) Probe(ctx context.Context) (ProbeResult, error) {
	result, err := a.probe(ctx)
	if err != nil {
		return ProbeResult{}, err
	}
	result.Runtime = a.runtime
	result.SupportedProviders = append([]ProviderOption(nil), a.providers...)
	if result.SuggestedProvider == "" && len(result.SupportedProviders) > 0 {
		result.SuggestedProvider = result.SupportedProviders[0].ID
	}
	return result, nil
}

func (a *adapter) Models(provider string) []ModelOption {
	return append([]ModelOption(nil), a.models[provider]...)
}

func (a *adapter) ValidateProvider(provider string) error {
	for _, option := range a.providers {
		if option.ID == provider {
			return nil
		}
	}
	return fmt.Errorf("provider %q is not supported for runtime %s", provider, a.runtime)
}

func (a *adapter) ValidateModel(provider, model string) error {
	if err := a.ValidateProvider(provider); err != nil {
		return err
	}
	for _, option := range a.models[provider] {
		if option.ID == model {
			return nil
		}
	}
	return fmt.Errorf("model %q is not supported for provider %s on runtime %s", model, provider, a.runtime)
}

func (a *adapter) SupportsNativeLocal() bool  { return a.localOnly }
func (a *adapter) SupportsNativeRemote() bool { return a.remoteOkay }

func New(runtimeName string) (Adapter, error) {
	switch runtimeName {
	case "pi":
		return newPIAdapter(defaultExec), nil
	case "claude-code":
		return newClaudeAdapter(defaultExec), nil
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeName)
	}
}

func CredentialRefForProvider(provider string) string {
	return canonicalCredentialRef(provider)
}

func ValidateBinding(adapter Adapter, binding config.RuntimeAuthConfig, sandboxProvider string) error {
	if binding.Mode == "" {
		return nil
	}
	if binding.Mode != ModeNative && binding.Mode != ModeTack {
		return fmt.Errorf("runtime_auth.mode must be %q or %q", ModeNative, ModeTack)
	}
	if err := adapter.ValidateProvider(binding.Provider); err != nil {
		return err
	}
	if binding.Mode == ModeNative {
		if sandboxProvider != "local" && !adapter.SupportsNativeRemote() {
			return fmt.Errorf("runtime_auth.mode=native is only supported with local sandboxes for runtime %s", binding.Runtime)
		}
		if sandboxProvider == "local" && !adapter.SupportsNativeLocal() {
			return fmt.Errorf("runtime %s does not support native auth references", binding.Runtime)
		}
	}
	return nil
}

func InjectEnv(binding config.RuntimeAuthConfig, store *credentials.Store, logger *slog.Logger, envVars map[string]string) error {
	if binding.Mode == "" || binding.Mode == ModeNative {
		return nil
	}
	if store == nil {
		return fmt.Errorf("credentials store not available for runtime_auth.mode=tack")
	}
	ref := binding.CredentialRef
	if ref == "" {
		ref = canonicalCredentialRef(binding.Provider)
	}
	cred, err := store.ModelProvider(ref)
	if err != nil {
		return fmt.Errorf("resolving credential_ref %q: %w", ref, err)
	}
	for key, value := range envForResolvedProvider(binding.Provider, cred.Type, cred.Value) {
		envVars[key] = value
	}
	if len(envVars) == 0 && logger != nil {
		logger.Warn("runtime auth produced no environment variables", "provider", binding.Provider, "credential_ref", ref)
	}
	return nil
}

func newPIAdapter(execRunner execFunc) Adapter {
	var a *adapter
	a = &adapter{
		runtime: "pi",
		providers: []ProviderOption{
			{ID: "anthropic", Label: "Anthropic"},
			{ID: "openai-codex", Label: "OpenAI Codex"},
		},
		models: map[string][]ModelOption{
			"anthropic": {
				{ID: "claude-opus-4-1", Label: "claude-opus-4-1"},
				{ID: "claude-opus-4-6", Label: "claude-opus-4-6"},
				{ID: "claude-sonnet-4-5-20250929", Label: "claude-sonnet-4-5-20250929"},
				{ID: "claude-sonnet-4-6", Label: "claude-sonnet-4-6"},
				{ID: "claude-haiku-4-5", Label: "claude-haiku-4-5"},
			},
			"openai-codex": {
				{ID: "gpt-5.4", Label: "gpt-5.4"},
				{ID: "gpt-5.4-mini", Label: "gpt-5.4-mini"},
				{ID: "gpt-5.3-codex", Label: "gpt-5.3-codex"},
				{ID: "gpt-5.3-codex-spark", Label: "gpt-5.3-codex-spark"},
			},
		},
		localOnly:  true,
		remoteOkay: false,
		probe: func(ctx context.Context) (ProbeResult, error) {
			result := ProbeResult{RuntimeAvailable: commandAvailable("pi")}
			catalogProviders, catalogModels, err := loadPIModelCatalog(ctx, execRunner)
			if err == nil {
				a.providers = catalogProviders
				a.models = catalogModels
				result.SupportedProviders = append([]ProviderOption(nil), catalogProviders...)
				if len(catalogProviders) > 0 {
					result.SuggestedProvider = catalogProviders[0].ID
				}
			}
			nativeProviders, err := loadPINativeProviders()
			if err != nil {
				return result, err
			}
			filtered := filterSupportedProviders(nativeProviders, a.models)
			if len(filtered) > 0 {
				result.NativeAvailable = true
				result.NativeProviders = filtered
				result.SuggestedProvider = filtered[0]
				result.NativeDescription = fmt.Sprintf("Found Pi auth for %s", strings.Join(filtered, ", "))
			}
			_ = ctx
			return result, nil
		},
	}
	return a
}

func newClaudeAdapter(execRunner execFunc) Adapter {
	providers := []ProviderOption{{ID: "anthropic", Label: "Anthropic"}}
	models := map[string][]ModelOption{
		"anthropic": {
			{ID: "claude-opus-4-1", Label: "Claude Opus 4.1"},
			{ID: "claude-sonnet-4", Label: "Claude Sonnet 4"},
			{ID: "claude-haiku-4", Label: "Claude Haiku 4"},
		},
	}
	return &adapter{
		runtime:    "claude-code",
		providers:  providers,
		models:     models,
		localOnly:  true,
		remoteOkay: false,
		probe: func(ctx context.Context) (ProbeResult, error) {
			result := ProbeResult{RuntimeAvailable: commandAvailable("claude"), SuggestedProvider: "anthropic"}
			stdout, err := execRunner(ctx, "claude", "auth", "status")
			if err != nil {
				if !result.RuntimeAvailable {
					return result, nil
				}
				return result, nil
			}
			result.RuntimeAvailable = true
			var status claudeAuthStatus
			if err := json.Unmarshal(stdout, &status); err != nil {
				return result, nil
			}
			if status.LoggedIn {
				result.NativeAvailable = true
				result.NativeProviders = []string{"anthropic"}
				details := []string{"Found Claude auth"}
				if status.Email != "" {
					details = append(details, status.Email)
				}
				if status.AuthMethod != "" {
					details = append(details, status.AuthMethod)
				}
				result.NativeDescription = strings.Join(details, " via ")
			}
			return result, nil
		},
	}
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func loadPINativeProviders() ([]string, error) {
	path := piAuthPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading Pi auth store: %w", err)
	}
	var auth map[string]piAuthCredential
	if err := yaml.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parsing Pi auth store: %w", err)
	}
	providers := make([]string, 0, len(auth))
	for provider := range auth {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	return providers, nil
}

func piAuthPath() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		if strings.HasPrefix(dir, "~/") {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, dir[2:])
		}
		return filepath.Join(dir, "auth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent", "auth.json")
}

func filterSupportedProviders(providers []string, models map[string][]ModelOption) []string {
	filtered := make([]string, 0, len(providers))
	for _, provider := range providers {
		if _, ok := models[provider]; ok {
			filtered = append(filtered, provider)
		}
	}
	return filtered
}

func canonicalCredentialRef(provider string) string {
	if ref, ok := providerCredentialRefs[provider]; ok {
		return ref
	}
	return provider
}

func loadPIModelCatalog(ctx context.Context, execRunner execFunc) ([]ProviderOption, map[string][]ModelOption, error) {
	stdout, err := execRunner(ctx, "pi", "--list-models")
	if err != nil {
		return nil, nil, fmt.Errorf("listing Pi models: %w", err)
	}
	providers := map[string]ProviderOption{}
	models := map[string][]ModelOption{}
	for _, line := range strings.Split(string(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "provider" {
			continue
		}
		providerID := fields[0]
		modelID := fields[1]
		if _, ok := providers[providerID]; !ok {
			providers[providerID] = ProviderOption{ID: providerID, Label: providerLabel(providerID)}
		}
		models[providerID] = append(models[providerID], ModelOption{ID: modelID, Label: modelID})
	}
	providerList := make([]ProviderOption, 0, len(providers))
	for _, provider := range providers {
		providerList = append(providerList, provider)
	}
	slices.SortFunc(providerList, func(a, b ProviderOption) int {
		return strings.Compare(a.Label, b.Label)
	})
	return providerList, models, nil
}

func providerLabel(providerID string) string {
	labels := map[string]string{
		"anthropic":    "Anthropic",
		"openai":       "OpenAI",
		"openai-codex": "OpenAI Codex",
		"gemini":       "Gemini",
		"google":       "Google",
		"groq":         "Groq",
		"mistral":      "Mistral",
		"xai":          "xAI",
	}
	if label, ok := labels[providerID]; ok {
		return label
	}
	return providerID
}

func envForResolvedProvider(provider, credType, value string) map[string]string {
	result := map[string]string{}
	switch provider {
	case "anthropic":
		switch credType {
		case credentials.TypeAPIKey:
			result["ANTHROPIC_API_KEY"] = value
		case credentials.TypeSetupToken, credentials.TypeOAuth:
			result["ANTHROPIC_OAUTH_TOKEN"] = value
		}
	case "openai":
		result["OPENAI_API_KEY"] = value
	case "openai-codex":
		result["OPENAI_API_KEY"] = value
	case "gemini":
		result["GEMINI_API_KEY"] = value
	case "groq":
		result["GROQ_API_KEY"] = value
	case "mistral":
		result["MISTRAL_API_KEY"] = value
	case "xai":
		result["XAI_API_KEY"] = value
	}
	return result
}
