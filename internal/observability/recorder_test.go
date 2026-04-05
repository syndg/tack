package observability

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/syndg/tack/internal/domain"
)

type stubPublisher struct {
	events []domain.Event
}

func (s *stubPublisher) Publish(event domain.Event) {
	s.events = append(s.events, event)
}

func TestRecordMilestone_WritesCanonicalEnvelopeAndProjectsEvent(t *testing.T) {
	pub := &stubPublisher{}
	recorder, err := New(t.TempDir(), pub, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recorder.RecordMilestone(Milestone{
		EventType:   domain.EventObjectiveCreated,
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		Details: map[string]any{
			"description": "Ship it",
		},
	})

	records := readProjectRecords(t, recorder, "proj-1")
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.ProjectID != "proj-1" || rec.ObjectiveID != "obj-1" {
		t.Fatalf("unexpected correlation fields: %+v", rec)
	}
	if rec.StreamID != "" || rec.AgentID != "" || rec.Role != "" {
		t.Fatalf("expected empty envelope fields to be preserved, got %+v", rec)
	}
	if rec.Kind != string(domain.EventObjectiveCreated) {
		t.Fatalf("kind = %q, want %q", rec.Kind, domain.EventObjectiveCreated)
	}
	if rec.Summary == "" || rec.Status == "" {
		t.Fatalf("expected derived summary/status, got %+v", rec)
	}

	if len(pub.events) != 1 {
		t.Fatalf("projected events = %d, want 1", len(pub.events))
	}
	if pub.events[0].Type != domain.EventObjectiveCreated {
		t.Fatalf("event type = %q, want %q", pub.events[0].Type, domain.EventObjectiveCreated)
	}

	raw := readFirstLine(t, ProjectLogPath(recorder.LogDir(), "proj-1"))
	var envelope map[string]any
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	for _, key := range []string{"project_id", "objective_id", "stream_id", "agent_id", "role", "kind", "summary", "status", "details", "ts"} {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("expected envelope key %q in %v", key, envelope)
		}
	}
}

func TestRecordActivity_ProjectsOnlyNotableActivity(t *testing.T) {
	pub := &stubPublisher{}
	recorder, err := New(t.TempDir(), pub, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recorder.RecordActivity(Activity{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		StreamID:    "stream-1",
		AgentID:     "agent-1",
		Role:        "builder",
		Kind:        "tool_start",
		Tool:        "Read",
		Content:     `{"file_path":"src/main.go"}`,
	})
	recorder.RecordActivity(Activity{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		StreamID:    "stream-1",
		AgentID:     "agent-1",
		Role:        "builder",
		Kind:        "tool_end",
		Tool:        "bash",
		Content:     `{"command":"bun test"}`,
		Duration:    1500 * time.Millisecond,
		IsError:     true,
	})

	records := readProjectRecords(t, recorder, "proj-1")
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Kind != "agent.tool_start" || records[1].Kind != "agent.tool_end" {
		t.Fatalf("unexpected activity kinds: %+v", records)
	}
	if got := int64FromDetail(records[1].Details["duration"]); got != 1500 {
		t.Fatalf("duration = %d, want 1500", got)
	}
	if len(pub.events) != 2 {
		t.Fatalf("projected events = %d, want 2", len(pub.events))
	}
	if pub.events[0].Type != domain.EventAgentActivity || pub.events[1].Type != domain.EventAgentActivity {
		t.Fatalf("unexpected projected event types: %+v", pub.events)
	}
}

func TestRecordActivity_ProjectsNormalToolAndMessageActivity(t *testing.T) {
	pub := &stubPublisher{}
	recorder, err := New(t.TempDir(), pub, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recorder.RecordActivity(Activity{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		StreamID:    "stream-1",
		AgentID:     "agent-1",
		Role:        "builder",
		Kind:        "tool_start",
		Tool:        "Read",
		Content:     `{"file_path":"src/main.go"}`,
	})
	recorder.RecordActivity(Activity{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		StreamID:    "stream-1",
		AgentID:     "agent-1",
		Role:        "builder",
		Kind:        "message",
		Content:     "implemented handler",
	})

	if len(pub.events) != 2 {
		t.Fatalf("projected events = %d, want 2", len(pub.events))
	}
	if pub.events[0].Type != domain.EventAgentActivity || pub.events[1].Type != domain.EventAgentActivity {
		t.Fatalf("unexpected projected event types: %+v", pub.events)
	}
}

func TestRecorder_StreamLifecycleScenarioPreservesReplayAndTimeline(t *testing.T) {
	pub := &stubPublisher{}
	recorder, err := New(t.TempDir(), pub, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recorder.RecordMilestone(Milestone{EventType: domain.EventAgentSpawned, ProjectID: "proj-1", ObjectiveID: "obj-1", StreamID: "stream-1", AgentID: "agent-1", Role: "builder", Details: map[string]any{"session_id": "agent-1"}})
	recorder.RecordActivity(Activity{ProjectID: "proj-1", ObjectiveID: "obj-1", StreamID: "stream-1", AgentID: "agent-1", Role: "builder", Kind: "tool_start", Tool: "Read", Content: `{"file_path":"src/main.go"}`})
	recorder.RecordActivity(Activity{ProjectID: "proj-1", ObjectiveID: "obj-1", StreamID: "stream-1", AgentID: "agent-1", Role: "builder", Kind: "message", Content: "implemented handler"})
	recorder.RecordMilestone(Milestone{EventType: domain.EventAgentCompleted, ProjectID: "proj-1", ObjectiveID: "obj-1", StreamID: "stream-1", AgentID: "agent-1", Role: "builder", Details: map[string]any{"summary": "implemented handler"}})
	recorder.RecordMilestone(Milestone{EventType: domain.EventMergeCompleted, ProjectID: "proj-1", ObjectiveID: "obj-1", StreamID: "stream-1", Details: map[string]any{"branch": "tack/obj/stream-1"}})

	records := readProjectRecords(t, recorder, "proj-1")
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5", len(records))
	}
	for i, want := range []string{"agent.spawned", "agent.tool_start", "agent.message", "agent.completed", "merge.completed"} {
		if records[i].Kind != want {
			t.Fatalf("record[%d].Kind = %q, want %q", i, records[i].Kind, want)
		}
	}
	if len(pub.events) != 5 {
		t.Fatalf("projected events = %d, want 5", len(pub.events))
	}
	for i, want := range []domain.EventType{domain.EventAgentSpawned, domain.EventAgentActivity, domain.EventAgentActivity, domain.EventAgentCompleted, domain.EventMergeCompleted} {
		if pub.events[i].Type != want {
			t.Fatalf("event[%d].Type = %q, want %q", i, pub.events[i].Type, want)
		}
	}
}

func readProjectRecords(t *testing.T, recorder *Recorder, projectID string) []Record {
	t.Helper()
	r, err := recorder.OpenReader(projectID)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer func() { _ = r.Close() }()

	var records []Record
	s := bufio.NewScanner(r)
	for s.Scan() {
		var rec Record
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			t.Fatalf("Unmarshal record: %v", err)
		}
		records = append(records, rec)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return records
}

func readFirstLine(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := string(data)
	if idx := len(line); idx > 0 && line[idx-1] == '\n' {
		line = line[:idx-1]
	}
	return line
}

func int64FromDetail(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
