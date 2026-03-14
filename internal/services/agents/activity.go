package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ActivityEvent represents a single agent activity log entry.
type ActivityEvent struct {
	Timestamp time.Time `json:"ts"`
	AgentID   string    `json:"agent_id"`
	Kind      string    `json:"kind"`     // "tool_start", "tool_end", "message", "error"
	Tool      string    `json:"tool"`     // tool name (empty for message)
	Content   string    `json:"content"`  // tool args, message text, or error
	Duration  int       `json:"duration"` // ms, only on tool_end
	IsError   bool      `json:"is_error"`
}

// ActivityLogger writes per-agent activity to JSONL files.
type ActivityLogger struct {
	logDir string
	mu     sync.Mutex
	files  map[string]*os.File
	logger *slog.Logger
}

// NewActivityLogger creates a logger that writes to the given directory.
func NewActivityLogger(logDir string, logger *slog.Logger) (*ActivityLogger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}
	return &ActivityLogger{
		logDir: logDir,
		files:  make(map[string]*os.File),
		logger: logger,
	}, nil
}

// Log writes an activity event to the agent's JSONL file.
func (l *ActivityLogger) Log(event ActivityEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, ok := l.files[event.AgentID]
	if !ok {
		var err error
		path := filepath.Join(l.logDir, event.AgentID+".jsonl")
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			l.logger.Error("failed to open activity log", "agent", event.AgentID, "error", err)
			return
		}
		l.files[event.AgentID] = f
	}

	data, err := json.Marshal(event)
	if err != nil {
		l.logger.Error("failed to marshal activity event", "error", err)
		return
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		l.logger.Error("failed to write activity event", "agent", event.AgentID, "error", err)
	}
}

// Close closes all open log file handles.
func (l *ActivityLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, f := range l.files {
		f.Close()
		delete(l.files, id)
	}
}

// CloseAgent closes the log file for a specific agent.
func (l *ActivityLogger) CloseAgent(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := l.files[agentID]; ok {
		f.Close()
		delete(l.files, agentID)
	}
}

// OpenReader returns a reader for an agent's activity log file.
// Returns nil, nil if the file doesn't exist.
func (l *ActivityLogger) OpenReader(agentID string) (io.ReadCloser, error) {
	path := filepath.Join(l.logDir, agentID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening activity log: %w", err)
	}
	return f, nil
}

// LogDir returns the directory where logs are stored.
func (l *ActivityLogger) LogDir() string {
	return l.logDir
}
