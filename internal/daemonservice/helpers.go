package daemonservice

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func wrapCommand(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

func userConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return dir
	}
	return filepath.Join(userHomeDir(), ".config")
}

func linuxUserConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(userHomeDir(), ".config")
}

func userUID() string {
	return strconv.Itoa(os.Getuid())
}

func xmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	return value
}

func systemdQuote(value string) string {
	return strconv.Quote(value)
}

func systemdEscapeEnv(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

func firstLines(value string, max int) []string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) > max {
		lines = lines[:max]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return lines
}
