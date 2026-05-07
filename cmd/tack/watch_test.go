package main

import (
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

func TestFormatWatchEventSummaryShowsAgentProgress(t *testing.T) {
	oldSummary := watchSummary
	t.Cleanup(func() { watchSummary = oldSummary })
	watchSummary = true

	line := formatWatchEvent(domain.Event{
		Type:      domain.EventAgentActivity,
		Stream:    "stream-1234567890",
		Agent:     "agent-1234567890",
		CreatedAt: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC),
		Payload:   `{"kind":"agent.tool_start","role":"builder","summary":"bash: go test ./pkg/gui/..."}`,
	})

	if !strings.Contains(line, "builder: bash: go test ./pkg/gui/...") {
		t.Fatalf("summary line = %q", line)
	}
}

func TestFormatWatchEventDistinguishesLocalMergeFromPublication(t *testing.T) {
	local := formatWatchEvent(domain.Event{
		Type:      domain.EventMergeCompleted,
		Stream:    "stream-1234567890",
		CreatedAt: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC),
		Payload:   `{"summary":"local merge completed"}`,
	})
	published := formatWatchEvent(domain.Event{
		Type:      domain.EventMergePublished,
		Stream:    "stream-1234567890",
		CreatedAt: time.Date(2026, 4, 29, 12, 0, 1, 0, time.UTC),
		Payload:   `{"summary":"merge branch published"}`,
	})

	if strings.Contains(local, "✓") {
		t.Fatalf("local merge line = %q, did not want publication marker", local)
	}
	if !strings.Contains(published, "✓") {
		t.Fatalf("published line = %q, want publication marker", published)
	}
}
