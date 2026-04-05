package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/syndg/tack/internal/domain"
)

// EventPublisher persists and broadcasts projected operator events.
type EventPublisher interface {
	Publish(event domain.Event)
}

// Record is the canonical operator-facing observability record.
type Record struct {
	Timestamp   time.Time      `json:"ts"`
	ProjectID   string         `json:"project_id"`
	ObjectiveID string         `json:"objective_id"`
	StreamID    string         `json:"stream_id"`
	AgentID     string         `json:"agent_id"`
	Role        string         `json:"role"`
	Kind        string         `json:"kind"`
	Summary     string         `json:"summary"`
	Status      string         `json:"status"`
	Details     map[string]any `json:"details"`

	eventType domain.EventType
	project   bool
}

// Milestone is a domain fact that should become a canonical record.
type Milestone struct {
	EventType   domain.EventType
	ProjectID   string
	ObjectiveID string
	StreamID    string
	AgentID     string
	Role        string
	Status      string
	Summary     string
	Details     map[string]any
}

// Activity is a fine-grained agent activity fact.
type Activity struct {
	ProjectID   string
	ObjectiveID string
	StreamID    string
	AgentID     string
	Role        string
	Kind        string
	Tool        string
	Content     string
	Duration    time.Duration
	IsError     bool
}

// Recorder owns canonical observability normalization, projection, and persistence.
type Recorder struct {
	logDir   string
	publish  EventPublisher
	logger   *slog.Logger
	mu       sync.Mutex
	projects map[string]*os.File
}

// New creates a Recorder rooted at the given log directory.
func New(logDir string, publish EventPublisher, logger *slog.Logger) (*Recorder, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating observability log dir: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{
		logDir:   logDir,
		publish:  publish,
		logger:   logger.With("component", "observability"),
		projects: make(map[string]*os.File),
	}, nil
}

// RecordMilestone records a stream/objective/operator milestone.
func (r *Recorder) RecordMilestone(input Milestone) {
	r.record(normalizeMilestone(input))
}

// RecordActivity records a fine-grained agent activity fact.
func (r *Recorder) RecordActivity(input Activity) {
	r.record(normalizeActivity(input))
}

// OpenReader returns a reader for the project's canonical JSONL stream.
func (r *Recorder) OpenReader(projectID string) (io.ReadCloser, error) {
	path := ProjectLogPath(r.logDir, projectID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening observability log: %w", err)
	}
	return f, nil
}

// LogDir returns the observability log root.
func (r *Recorder) LogDir() string {
	return r.logDir
}

// Close closes any cached project log file handles.
func (r *Recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for projectID, f := range r.projects {
		_ = f.Close()
		delete(r.projects, projectID)
	}
}

// ProjectLogPath returns the canonical JSONL path for a project timeline.
func ProjectLogPath(logDir, projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = "unknown"
	}
	return filepath.Join(logDir, projectID+".jsonl")
}

func (r *Recorder) record(record Record) {
	if record.Details == nil {
		record.Details = map[string]any{}
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if err := r.writeJSONL(record); err != nil {
		r.logger.Error("writing canonical record", "project_id", record.ProjectID, "kind", record.Kind, "error", err)
	}
	if record.project {
		r.publishProjected(record)
	}
}

func (r *Recorder) writeJSONL(record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling canonical record: %w", err)
	}
	data = append(data, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()

	projectID := strings.TrimSpace(record.ProjectID)
	f, ok := r.projects[projectID]
	if !ok {
		path := ProjectLogPath(r.logDir, projectID)
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("opening canonical log file: %w", err)
		}
		r.projects[projectID] = f
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("appending canonical record: %w", err)
	}
	return nil
}

func (r *Recorder) publishProjected(record Record) {
	if r.publish == nil || record.eventType == "" {
		return
	}
	payload := map[string]any{
		"kind":    record.Kind,
		"summary": record.Summary,
		"status":  record.Status,
		"role":    record.Role,
	}
	for k, v := range record.Details {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		r.logger.Error("marshaling projected event payload", "kind", record.Kind, "error", err)
		return
	}
	r.publish.Publish(domain.Event{
		ProjectID: record.ProjectID,
		Type:      record.eventType,
		Objective: record.ObjectiveID,
		Stream:    record.StreamID,
		Agent:     record.AgentID,
		Payload:   string(data),
		CreatedAt: record.Timestamp,
	})
}

func normalizeMilestone(input Milestone) Record {
	details := cloneDetails(input.Details)
	kind := string(input.EventType)
	status := strings.TrimSpace(input.Status)
	summary := strings.TrimSpace(input.Summary)
	if status == "" {
		status = milestoneStatus(input.EventType, details)
	}
	if summary == "" {
		summary = milestoneSummary(input.EventType, details)
	}
	return Record{
		Timestamp:   time.Now(),
		ProjectID:   input.ProjectID,
		ObjectiveID: input.ObjectiveID,
		StreamID:    input.StreamID,
		AgentID:     input.AgentID,
		Role:        input.Role,
		Kind:        kind,
		Summary:     summary,
		Status:      status,
		Details:     details,
		eventType:   input.EventType,
		project:     true,
	}
}

func normalizeActivity(input Activity) Record {
	details := map[string]any{
		"tool":     input.Tool,
		"content":  input.Content,
		"duration": input.Duration.Milliseconds(),
		"is_error": input.IsError,
	}
	kind := activityKind(input.Kind)
	status := "info"
	if input.IsError || input.Kind == "error" {
		status = "failed"
	}
	return Record{
		Timestamp:   time.Now(),
		ProjectID:   input.ProjectID,
		ObjectiveID: input.ObjectiveID,
		StreamID:    input.StreamID,
		AgentID:     input.AgentID,
		Role:        input.Role,
		Kind:        kind,
		Summary:     activitySummary(input),
		Status:      status,
		Details:     details,
		eventType:   domain.EventAgentActivity,
		project:     projectActivity(input),
	}
}

func cloneDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func milestoneStatus(eventType domain.EventType, details map[string]any) string {
	if v := stringValue(details["to"]); v != "" {
		return v
	}
	if v := stringValue(details["plan_status"]); v != "" {
		return v
	}
	switch eventType {
	case domain.EventObjectiveCreated:
		return "created"
	case domain.EventPlanCreated:
		return "created"
	case domain.EventPlanApproved:
		return "approved"
	case domain.EventExecutionStarted:
		return "started"
	case domain.EventStreamReady:
		return "ready"
	case domain.EventAgentSpawned:
		return "spawned"
	case domain.EventAgentCompleted:
		return "completed"
	case domain.EventAgentFailed:
		return "failed"
	case domain.EventMergeQueued:
		return "queued"
	case domain.EventMergeCompleted:
		return "merged"
	case domain.EventMergeFailed:
		return "failed"
	case domain.EventEscalation:
		return "escalated"
	case domain.EventMailSent:
		return "sent"
	default:
		return "info"
	}
}

func milestoneSummary(eventType domain.EventType, details map[string]any) string {
	switch eventType {
	case domain.EventObjectiveCreated:
		return "objective created"
	case domain.EventObjectiveUpdated:
		from := stringValue(details["from"])
		to := stringValue(details["to"])
		if from != "" && to != "" {
			return fmt.Sprintf("objective %s -> %s", from, to)
		}
		if to != "" {
			return fmt.Sprintf("objective -> %s", to)
		}
		if planStatus := stringValue(details["plan_status"]); planStatus != "" {
			return fmt.Sprintf("plan %s", planStatus)
		}
		return "objective updated"
	case domain.EventPlanCreated:
		return "plan created"
	case domain.EventPlanApproved:
		return "plan approved"
	case domain.EventExecutionStarted:
		return "execution started"
	case domain.EventStreamReady:
		return "stream ready"
	case domain.EventAgentSpawned:
		return "agent spawned"
	case domain.EventAgentCompleted:
		return "agent completed"
	case domain.EventAgentFailed:
		if reason := stringValue(details["reason"]); reason != "" {
			return fmt.Sprintf("agent failed: %s", reason)
		}
		return "agent failed"
	case domain.EventMailSent:
		return "mail sent"
	case domain.EventMergeQueued:
		return "merge queued"
	case domain.EventMergeCompleted:
		return "merged"
	case domain.EventMergeFailed:
		return "merge failed"
	case domain.EventEscalation:
		return "escalation"
	default:
		return string(eventType)
	}
}

func activityKind(kind string) string {
	switch kind {
	case "tool_start":
		return "agent.tool_start"
	case "tool_end":
		return "agent.tool_end"
	case "message":
		return "agent.message"
	case "error":
		return "agent.error"
	default:
		return "agent.activity"
	}
}

func projectActivity(input Activity) bool {
	switch input.Kind {
	case "tool_start", "tool_end", "message", "error":
		return true
	default:
		return input.IsError
	}
}

func activitySummary(input Activity) string {
	switch input.Kind {
	case "tool_start":
		return toolSummary(input.Tool, input.Content)
	case "tool_end":
		summary := toolSummary(input.Tool, input.Content)
		if summary == "" {
			summary = input.Tool
		}
		if input.IsError {
			return summary + " failed"
		}
		return summary + " done"
	case "message":
		return truncate(input.Content, 120)
	case "error":
		return truncate(input.Content, 120)
	default:
		return truncate(input.Content, 120)
	}
}

func toolSummary(tool, content string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return "activity"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(content), &args); err != nil {
		if idx := strings.Index(content, ": "); idx > 0 {
			raw := content[idx+2:]
			if err2 := json.Unmarshal([]byte(raw), &args); err2 != nil {
				return tool
			}
		} else {
			return tool
		}
	}
	switch tool {
	case "read", "Read":
		if p := stringValue(args["path"]); p != "" {
			return "read " + p
		}
		if p := stringValue(args["file_path"]); p != "" {
			return "read " + p
		}
	case "edit", "Edit":
		if p := stringValue(args["path"]); p != "" {
			return "edit " + p
		}
		if p := stringValue(args["file_path"]); p != "" {
			return "edit " + p
		}
	case "write", "Write":
		if p := stringValue(args["path"]); p != "" {
			return "write " + p
		}
		if p := stringValue(args["file_path"]); p != "" {
			return "write " + p
		}
	case "bash", "Bash":
		if cmd := stringValue(args["command"]); cmd != "" {
			return "$ " + truncate(cmd, 80)
		}
	case "tack_done":
		if s := stringValue(args["summary"]); s != "" {
			return "done: " + truncate(s, 80)
		}
	case "tack_escalate":
		if reason := stringValue(args["reason"]); reason != "" {
			return "escalate: " + truncate(reason, 80)
		}
	}
	return tool
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
