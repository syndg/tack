package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/runtime"
	events "github.com/syndg/tack/internal/services/events"
)

// AgentTracker manages the lifecycle of live agent processes.
// Thread-safe: called concurrently from execution goroutines and HTTP handlers.
type AgentTracker interface {
	Track(session *domain.AgentSession, process runtime.AgentProcess)
	Finish(sessionID string) (wasKilled bool)
	Kill(ctx context.Context, sessionID string) error
	StopAll()
	Count() int
}

// agentTracker is the concrete implementation of AgentTracker.
type agentTracker struct {
	spawner   *Spawner
	obs       *observability.Recorder
	eventBus  *events.PersistentBus
	timeouts  config.TimeoutConfig
	logger    *slog.Logger
	timeScale time.Duration // unit for timeout values; defaults to time.Minute

	mu         sync.Mutex
	agentMap   map[string]*SpawnResult // sessionID → spawn result
	terminated map[string]bool         // sessionID → explicitly killed
	idleTimers map[string]*time.Timer  // sessionID → idle timeout timer
}

// Compile-time check that *agentTracker satisfies AgentTracker.
var _ AgentTracker = (*agentTracker)(nil)

// newAgentTracker creates a new AgentTracker.
func newAgentTracker(
	spawner *Spawner,
	obs *observability.Recorder,
	eventBus *events.PersistentBus,
	timeouts config.TimeoutConfig,
	logger *slog.Logger,
) *agentTracker {
	return &agentTracker{
		spawner:    spawner,
		obs:        obs,
		eventBus:   eventBus,
		timeouts:   timeouts,
		logger:     logger,
		timeScale:  time.Minute,
		agentMap:   make(map[string]*SpawnResult),
		terminated: make(map[string]bool),
		idleTimers: make(map[string]*time.Timer),
	}
}

// Track registers a spawn result so the agent can be killed or monitored.
// Also starts timeout timers and a goroutine to drain agent activity events.
func (t *agentTracker) Track(session *domain.AgentSession, process runtime.AgentProcess) {
	result := &SpawnResult{Session: session, Process: process}

	t.mu.Lock()
	t.agentMap[session.ID] = result
	delete(t.terminated, session.ID)
	t.mu.Unlock()

	t.drainAgentActivity(session, process)
}

// Finish removes the agent from tracking and returns whether it was killed.
func (t *agentTracker) Finish(sessionID string) (wasKilled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.agentMap, sessionID)
	wasKilled = t.terminated[sessionID]
	delete(t.terminated, sessionID)
	return wasKilled
}

// Kill terminates a live agent process. If the session is not tracked,
// falls back to spawner.Kill which looks it up in the database.
func (t *agentTracker) Kill(ctx context.Context, sessionID string) error {
	t.mu.Lock()
	result, ok := t.agentMap[sessionID]
	if ok {
		t.terminated[sessionID] = true
	}
	t.mu.Unlock()

	if !ok {
		return t.spawner.Kill(ctx, sessionID)
	}

	if err := result.Process.Kill(); err != nil {
		return fmt.Errorf("killing agent process %s: %w", sessionID, err)
	}
	if t.spawner != nil {
		t.spawner.MarkFailed(ctx, result.Session, "killed")
	}
	return nil
}

// StopAll kills all tracked agents.
func (t *agentTracker) StopAll() {
	t.mu.Lock()
	agents := make([]*SpawnResult, 0, len(t.agentMap))
	for _, result := range t.agentMap {
		agents = append(agents, result)
		t.terminated[result.Session.ID] = true
	}
	t.mu.Unlock()

	for _, result := range agents {
		if err := result.Process.Kill(); err != nil {
			t.logger.Warn("failed to kill agent on stop",
				"session_id", result.Session.ID,
				"error", err,
			)
		}
	}
}

// Count returns the number of currently tracked agents.
func (t *agentTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.agentMap)
}

// drainAgentActivity starts a goroutine that reads agent output events and
// publishes them to the event bus + writes to the activity JSONL log.
// Also starts timeout timers (max duration + idle) for the agent.
func (t *agentTracker) drainAgentActivity(session *domain.AgentSession, process runtime.AgentProcess) {
	role := string(session.Role)
	rt := t.timeouts.GetTimeout(role)

	// Start max duration timer
	if rt.MaxDurationMinutes > 0 {
		dur := time.Duration(rt.MaxDurationMinutes) * t.timeScale
		time.AfterFunc(dur, func() {
			t.mu.Lock()
			_, stillActive := t.agentMap[session.ID]
			if stillActive {
				t.terminated[session.ID] = true
			}
			t.mu.Unlock()
			if stillActive {
				t.logger.Warn("agent max duration exceeded, killing",
					"session_id", session.ID,
					"role", role,
					"max_minutes", rt.MaxDurationMinutes,
				)
				_ = process.Kill()
			}
		})
	}

	// Start idle timer
	var idleTimer *time.Timer
	if rt.IdleMinutes > 0 {
		idleDur := time.Duration(rt.IdleMinutes) * t.timeScale
		idleTimer = time.AfterFunc(idleDur, func() {
			t.mu.Lock()
			_, stillActive := t.agentMap[session.ID]
			if stillActive {
				t.terminated[session.ID] = true
			}
			t.mu.Unlock()
			if stillActive {
				t.logger.Warn("agent idle timeout, killing",
					"session_id", session.ID,
					"role", role,
					"idle_minutes", rt.IdleMinutes,
				)
				_ = process.Kill()
			}
		})
		t.mu.Lock()
		t.idleTimers[session.ID] = idleTimer
		t.mu.Unlock()
	}

	go func() {
		for event := range process.Output() {
			// Reset idle timer on any activity
			if idleTimer != nil {
				idleTimer.Reset(time.Duration(rt.IdleMinutes) * t.timeScale)
			}

			kind := event.Type
			tool := ""
			switch event.Type {
			case "tool_call":
				kind = "tool_start"
				if idx := strings.Index(event.Content, ": "); idx > 0 {
					tool = event.Content[:idx]
				} else {
					tool = event.Content
				}
			case "tool_end":
				kind = "tool_end"
				tool = strings.TrimSuffix(event.Content, " (failed)")
			case "output":
				kind = "message"
			case "error":
				kind = "error"
			}

			if t.obs != nil {
				t.obs.RecordActivity(observability.Activity{
					ProjectID:   session.ProjectID,
					ObjectiveID: session.ObjectiveID,
					StreamID:    session.StreamID,
					AgentID:     session.ID,
					Role:        string(session.Role),
					Kind:        kind,
					Tool:        tool,
					Content:     event.Content,
					IsError:     event.IsError,
				})
			}

			t.eventBus.Emit(domain.EventAgentActivity, session.ObjectiveID, session.StreamID, session.ID,
				"kind", kind,
				"tool", tool,
				"content", truncateForEvent(event.Content, 500),
				"is_error", event.IsError,
			)
		}

		// Cleanup: stop idle timer and close log file
		if idleTimer != nil {
			idleTimer.Stop()
			t.mu.Lock()
			delete(t.idleTimers, session.ID)
			t.mu.Unlock()
		}
	}()
}
