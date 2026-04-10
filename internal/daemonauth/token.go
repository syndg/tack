package daemonauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvToken     = "TACK_DAEMON_TOKEN"
	EnvTokenPath = "TACK_DAEMON_TOKEN_PATH"
	defaultPath  = "~/.config/tack/daemon-token"
	agentPrefix  = "agt1"
)

type AgentClaims struct {
	ProjectID   string `json:"project_id"`
	ObjectiveID string `json:"objective_id"`
	StreamID    string `json:"stream_id,omitempty"`
	AgentName   string `json:"agent_name"`
	Role        string `json:"role,omitempty"`
}

func Load() (string, error) {
	if token := strings.TrimSpace(os.Getenv(EnvToken)); token != "" {
		return token, nil
	}
	data, err := os.ReadFile(TokenPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading daemon token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func LoadOrCreate() (string, error) {
	if token := strings.TrimSpace(os.Getenv(EnvToken)); token != "" {
		return token, nil
	}
	if token, err := Load(); err != nil || token != "" {
		return token, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating daemon token: %w", err)
	}
	token := hex.EncodeToString(raw)
	path := TokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("creating daemon token directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing daemon token: %w", err)
	}
	return token, nil
}

func TokenPath() string {
	path := strings.TrimSpace(os.Getenv(EnvTokenPath))
	if path == "" {
		path = defaultPath
	}
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

func IssueAgentToken(secret string, claims AgentClaims) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("agent token secret is required")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal agent claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return agentPrefix + "." + encodedPayload + "." + signature, nil
}

func ParseAgentToken(token, secret string) (*AgentClaims, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("agent token secret is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != agentPrefix {
		return nil, fmt.Errorf("invalid agent token format")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[1]))
	expectedSig := mac.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode agent token signature: %w", err)
	}
	if !hmac.Equal(gotSig, expectedSig) {
		return nil, fmt.Errorf("invalid agent token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode agent token payload: %w", err)
	}
	var claims AgentClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("decode agent token claims: %w", err)
	}
	if strings.TrimSpace(claims.ProjectID) == "" || strings.TrimSpace(claims.ObjectiveID) == "" || strings.TrimSpace(claims.AgentName) == "" {
		return nil, fmt.Errorf("agent token missing required claims")
	}
	return &claims, nil
}
