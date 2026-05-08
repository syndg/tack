package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"gopkg.in/yaml.v3"
)

func TestReloadEndpointReloadsSafeConfigInPlace(t *testing.T) {
	d, cfg := newReloadTestDaemon(t)
	server := httptest.NewServer(d.server.Handler)
	t.Cleanup(server.Close)

	c := client.New(server.URL)
	c.SetAuthToken(d.authToken)
	next := *cfg
	next.QualityGates = []string{"go test ./..."}
	writeReloadTestConfig(t, &next)

	if err := c.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := d.cfg.QualityGates; len(got) != 1 || got[0] != "go test ./..." {
		t.Fatalf("QualityGates = %v", got)
	}
}

func TestReloadEndpointRequiresRestartForUnsafeListenChange(t *testing.T) {
	d, cfg := newReloadTestDaemon(t)
	server := httptest.NewServer(d.server.Handler)
	t.Cleanup(server.Close)

	c := client.New(server.URL)
	c.SetAuthToken(d.authToken)
	next := *cfg
	next.Daemon.Listen = "127.0.0.1:19999"
	writeReloadTestConfig(t, &next)

	err := c.Reload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "restart required") {
		t.Fatalf("err = %v, want restart required", err)
	}
}

func newReloadTestDaemon(t *testing.T) (*Daemon, *config.Config) {
	t.Helper()
	t.Setenv("TACK_USER_CONFIG_PATH", filepath.Join(t.TempDir(), "config.yaml"))
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19888"
	cfg.Daemon.DataDir = t.TempDir()
	writeReloadTestConfig(t, cfg)
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Shutdown(context.Background())
	})
	return d, cfg
}

func writeReloadTestConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(os.Getenv("TACK_USER_CONFIG_PATH"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
