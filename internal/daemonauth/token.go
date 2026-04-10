package daemonauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvToken     = "TACK_DAEMON_TOKEN"
	EnvTokenPath = "TACK_DAEMON_TOKEN_PATH"
	defaultPath  = "~/.config/tack/daemon-token"
)

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
