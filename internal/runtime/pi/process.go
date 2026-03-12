package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
)

// PiProcess implements runtime.AgentProcess with JSONL-based RPC over a ProcessHandle.
type PiProcess struct {
	handle   sandbox.ProcessHandle
	outputCh chan runtime.AgentEvent
	doneCh   chan struct{}
	result   runtime.AgentResult
	mu       sync.Mutex
	killed   bool
	logger   *slog.Logger
}

func newPiProcess(handle sandbox.ProcessHandle, logger *slog.Logger) *PiProcess {
	p := &PiProcess{
		handle:   handle,
		outputCh: make(chan runtime.AgentEvent, 16),
		doneCh:   make(chan struct{}),
		logger:   logger,
	}
	go p.readLoop()
	return p
}

// readLoop reads JSONL events from the ProcessHandle and maps them to AgentEvents.
func (p *PiProcess) readLoop() {
	defer close(p.doneCh)
	defer close(p.outputCh)

	for {
		line, err := p.handle.ReadLine()
		if err != nil {
			if err == io.EOF {
				p.mu.Lock()
				if !p.result.Success && p.result.Error == "" {
					p.result = runtime.AgentResult{
						Success: true,
						Summary: "process ended",
					}
				}
				p.mu.Unlock()
				return
			}
			p.mu.Lock()
			p.result = runtime.AgentResult{
				Success: false,
				Error:   fmt.Sprintf("read error: %v", err),
			}
			p.mu.Unlock()
			p.emit(runtime.AgentEvent{Type: "error", Content: err.Error()})
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event PiEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			p.logger.Warn("skipping non-JSON line from pi", "line", line)
			continue
		}

		p.handleEvent(event)
	}
}

// handleEvent maps a PiEvent to a runtime.AgentEvent and updates result state.
func (p *PiProcess) handleEvent(event PiEvent) {
	switch event.Type {
	case PiEventOutput:
		p.emit(runtime.AgentEvent{Type: "output", Content: event.Content})

	case PiEventToolCall:
		content := event.Tool
		if event.Args != "" {
			content += ": " + event.Args
		}
		p.emit(runtime.AgentEvent{Type: "tool_call", Content: content})

	case PiEventError:
		p.emit(runtime.AgentEvent{Type: "error", Content: event.Content})

	case PiEventDone:
		p.mu.Lock()
		p.result = runtime.AgentResult{
			Success: event.Success,
			Summary: event.Summary,
		}
		if !event.Success && event.Content != "" {
			p.result.Error = event.Content
		}
		p.mu.Unlock()

		// Check for DECK_DONE signal in content or summary
		summary := event.Summary
		if strings.HasPrefix(event.Content, "DECK_DONE:") {
			summary = strings.TrimPrefix(event.Content, "DECK_DONE:")
			p.mu.Lock()
			p.result.Summary = summary
			p.mu.Unlock()
		}

		p.emit(runtime.AgentEvent{Type: "output", Content: summary})

	case PiEventStatus:
		p.logger.Info("pi status", "content", event.Content)
	}
}

func (p *PiProcess) emit(event runtime.AgentEvent) {
	select {
	case p.outputCh <- event:
	default:
		p.logger.Warn("pi output channel full, dropping event", "type", event.Type)
	}
}

// Send marshals an AgentMessage to a Pi RPC command and writes it to stdin.
func (p *PiProcess) Send(_ context.Context, msg runtime.AgentMessage) error {
	cmd := PiCommand{
		Type:    msg.Type,
		Content: msg.Content,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshaling command: %w", err)
	}
	data = append(data, '\n')
	return p.handle.Write(data)
}

// Output returns the channel that receives agent events.
func (p *PiProcess) Output() <-chan runtime.AgentEvent {
	return p.outputCh
}

// Wait blocks until the Pi process completes and returns the result.
func (p *PiProcess) Wait() (runtime.AgentResult, error) {
	<-p.doneCh
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result, nil
}

// Kill terminates the Pi process.
func (p *PiProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.killed {
		p.killed = true
		return p.handle.Kill()
	}
	return nil
}
