package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/syndg/deck/internal/client"
	"github.com/syndg/deck/internal/config"
)

func TestDaemonIntegration(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19800"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	waitForHTTP(t, "http://"+cfg.Daemon.Listen+"/health")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Post("http://"+cfg.Daemon.Listen+"/objectives", "application/json", bytes.NewBufferString(`{"description":"  test objective  "}`))
	if err != nil {
		t.Fatalf("POST /objectives: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	c := client.New("http://" + cfg.Daemon.Listen)
	objectives, err := c.ListObjectives(context.Background())
	if err != nil {
		t.Fatalf("ListObjectives: %v", err)
	}
	if len(objectives) != 1 {
		t.Fatalf("expected 1 objective, got %d", len(objectives))
	}
	if objectives[0].Description != "test objective" {
		t.Fatalf("expected trimmed description, got %q", objectives[0].Description)
	}

	status, err := c.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Status != "ok" {
		t.Fatalf("unexpected status: %q", status.Status)
	}
	if status.Objectives["planning"] != 1 {
		t.Fatalf("expected planning count 1, got %d", status.Objectives["planning"])
	}
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", url)
}

func TestCreateObjectiveValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Daemon.Listen = "127.0.0.1:19801"
	cfg.Daemon.DataDir = t.TempDir()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
		<-errCh
	}()

	waitForHTTP(t, "http://"+cfg.Daemon.Listen+"/health")

	resp, err := http.Post("http://"+cfg.Daemon.Listen+"/objectives", "application/json", bytes.NewBufferString(`{"description":"   "}`))
	if err != nil {
		t.Fatalf("POST /objectives: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if body["error"] == "" {
		t.Fatal("expected error message in response")
	}
}
