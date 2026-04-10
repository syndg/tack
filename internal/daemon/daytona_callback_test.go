package daemon

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/domain"
)

func TestDaemonURLIsLoopback(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "http://127.0.0.1:9800", want: true},
		{url: "http://localhost:9800", want: true},
		{url: "http://0.0.0.0:9800", want: true},
		{url: "http://[::1]:9800", want: true},
		{url: "https://tack.example.com", want: false},
		{url: "http://192.168.1.20:9800", want: false},
	}
	for _, tc := range tests {
		if got := daemonURLIsLoopback(tc.url); got != tc.want {
			t.Fatalf("daemonURLIsLoopback(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestDaytonaProviderRequiresReachableCallbackURL(t *testing.T) {
	creds, err := credentials.Load("")
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	mgr := &ProjectContextManager{
		daemonURL: "http://127.0.0.1:9800",
		creds:     creds,
		logger:    slog.Default(),
	}
	project := &domain.Project{ID: "proj-1", RootPath: t.TempDir()}
	cfg := config.Default()
	cfg.Sandbox.Provider = "daytona"
	cfg.Sandbox.Daytona.APIKey = "test-key"
	_, err = mgr.newSandboxProvider(project, cfg)
	if err == nil {
		t.Fatal("expected loopback callback validation error")
	}
	if !strings.Contains(err.Error(), "daemon.external_url") {
		t.Fatalf("error = %v, want daemon.external_url guidance", err)
	}
}
