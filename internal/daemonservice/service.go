package daemonservice

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	Name        = "tack-daemon"
	LaunchdID   = "com.syndg.tack.daemon"
	SystemdUnit = "tack-daemon.service"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type Provider interface {
	Install(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Reload(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
}

type Status struct {
	Provider   string
	Installed  bool
	Enabled    bool
	Running    bool
	Healthy    bool
	ConfigPath string
	LogPath    string
	Listen     string
	Details    []string
}

type Options struct {
	Executable string
	ConfigPath string
	Listen     string
	LogDir     string
	Runner     Runner
	GOOS       string
}

func New(opts Options) (Provider, error) {
	if opts.Runner == nil {
		opts.Runner = ExecRunner{}
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.Executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolving executable: %w", err)
		}
		opts.Executable = exe
	}
	if opts.LogDir == "" {
		opts.LogDir = filepath.Join(userCacheDir(), "tack")
	}
	switch opts.GOOS {
	case "darwin":
		return &launchdProvider{opts: opts}, nil
	case "linux":
		return &systemdProvider{opts: opts}, nil
	default:
		return nil, fmt.Errorf("daemon service is unsupported on %s", opts.GOOS)
	}
}

func healthCheck(ctx context.Context, listen string) bool {
	listen = normalizeListen(listen)
	if listen == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listen+"/health", nil)
	if err != nil {
		return false
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func normalizeListen(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
		return strings.TrimRight(listen, "/")
	}
	return "http://" + strings.TrimRight(listen, "/")
}

func userCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return dir
	}
	return os.TempDir()
}
