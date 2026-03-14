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
	hasError bool // set when RPC failures or extension errors occur
	logger   *slog.Logger

	// Accumulate assistant text output across streaming deltas
	textBuf strings.Builder

	// cleanup is called when the process finishes (e.g., remove temp extension dir)
	cleanup func()
}

func newPiProcess(handle sandbox.ProcessHandle, logger *slog.Logger) *PiProcess {
	p := &PiProcess{
		handle:   handle,
		outputCh: make(chan runtime.AgentEvent, 64),
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
				p.finalize()
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
			p.logger.Warn("skipping non-JSON line from pi", "line", line[:min(len(line), 200)])
			continue
		}

		p.handleEvent(event)
	}
}

// finalize sets a default result if nothing else was set, and runs cleanup.
// If RPC or extension errors occurred during the session, the result is
// marked as failed even if no agent_end event was received.
func (p *PiProcess) finalize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.result.Success && p.result.Error == "" {
		summary := strings.TrimSpace(p.textBuf.String())
		if summary == "" {
			summary = "process ended"
		}
		if p.hasError {
			p.result = runtime.AgentResult{
				Success: false,
				Summary: summary,
				Error:   "process ended with errors",
			}
		} else {
			p.result = runtime.AgentResult{
				Success: true,
				Summary: summary,
			}
		}
	}
	if p.cleanup != nil {
		p.cleanup()
		p.cleanup = nil
	}
}

// handleEvent maps a PiEvent to runtime.AgentEvent(s) and updates result state.
func (p *PiProcess) handleEvent(event PiEvent) {
	switch event.Type {
	case PiEventResponse:
		// Command acknowledgement from Pi (e.g., prompt accepted)
		if !event.Success {
			p.logger.Error("pi command failed", "command", event.Command, "error", event.Error)
			p.mu.Lock()
			p.hasError = true
			p.mu.Unlock()
			p.emit(runtime.AgentEvent{Type: "error", Content: fmt.Sprintf("command %s failed: %s", event.Command, event.Error)})
		} else {
			p.logger.Debug("pi command accepted", "command", event.Command)
		}

	case PiEventAgentStart:
		p.logger.Info("pi agent started")

	case PiEventMessageUpdate:
		// Streaming text delta from assistant — accumulate silently.
		// We don't emit individual deltas to avoid flooding the output channel.
		// The full text is available in the result when the agent ends.
		var ame AssistantMessageEvent
		if event.AssistantMessageEvent != nil {
			_ = json.Unmarshal(event.AssistantMessageEvent, &ame)
		}
		if ame.Type == "text_delta" && ame.Delta != "" {
			p.textBuf.WriteString(ame.Delta)
		}

	case PiEventMessageStart, PiEventMessageEnd:
		// We only care about the streaming deltas, not start/end markers.

	case PiEventToolExecStart:
		content := event.ToolName
		if event.Args != nil {
			content += ": " + string(event.Args)
		}
		p.emit(runtime.AgentEvent{Type: "tool_call", Content: content})

	case PiEventToolExecUpdate:
		// Streaming tool output — log but don't emit to coordinator

	case PiEventToolExecEnd:
		content := event.ToolName
		if event.IsError {
			content += " (failed)"
			p.logger.Warn("pi tool execution failed", "tool", event.ToolName, "id", event.ToolCallID)
		}
		p.emit(runtime.AgentEvent{Type: "tool_end", Content: content, IsError: event.IsError})

	case PiEventAgentEnd:
		// Agent finished — extract the final assistant text from accumulated output
		summary := strings.TrimSpace(p.textBuf.String())

		p.mu.Lock()
		if p.hasError {
			// RPC or extension errors occurred during this session —
			// don't mark as success even though agent_end was received.
			p.result = runtime.AgentResult{
				Success: false,
				Summary: summary,
				Error:   "agent ended with prior errors",
			}
		} else {
			p.result = runtime.AgentResult{
				Success: true,
				Summary: summary,
			}
		}

		// Check for DECK_DONE signal in accumulated text
		if idx := strings.Index(summary, "DECK_DONE:"); idx >= 0 {
			doneSummary := summary[idx+len("DECK_DONE:"):]
			if nlIdx := strings.IndexByte(doneSummary, '\n'); nlIdx >= 0 {
				doneSummary = doneSummary[:nlIdx]
			}
			p.result.Summary = strings.TrimSpace(doneSummary)
		}
		p.mu.Unlock()

		p.emit(runtime.AgentEvent{Type: "output", Content: summary})

		// Pi in RPC mode stays alive after agent_end, waiting for more commands.
		// Deck only needs one prompt-response cycle per agent, so kill the process
		// to trigger EOF → readLoop exit → doneCh close → Wait() unblocks.
		p.logger.Info("pi agent ended, killing process")
		go p.Kill()

	case PiEventExtensionError:
		p.logger.Error("pi extension error",
			"extension", event.ExtensionPath,
			"hook", event.Event,
			"error", event.Error,
		)
		p.mu.Lock()
		p.hasError = true
		p.mu.Unlock()
		p.emit(runtime.AgentEvent{Type: "error", Content: fmt.Sprintf("extension error: %s (hook: %s)", event.Error, event.Event), IsError: true})

	case PiEventTurnStart, PiEventTurnEnd:
		// Internal turn lifecycle — no action needed

	case PiEventAutoCompactionStart, PiEventAutoCompactionEnd:
		p.logger.Info("pi auto-compaction", "type", event.Type, "reason", event.Reason)

	case PiEventAutoRetryStart, PiEventAutoRetryEnd:
		p.logger.Info("pi auto-retry", "type", event.Type)

	default:
		p.logger.Debug("unhandled pi event", "type", event.Type)
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
		Message: msg.Content,
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
