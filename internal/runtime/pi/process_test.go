package pi

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/tack/internal/runtime"
)

func TestPiProcess_OutputEvent(t *testing.T) {
	// message_update deltas accumulate text; agent_end emits it as one output event.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello "}}`,
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"world"}}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Type != "output" || events[0].Content != "hello world" {
		t.Errorf("event[0] = %+v, want output/'hello world'", events[0])
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %+v", result)
	}
	if result.Summary != "hello world" {
		t.Errorf("summary = %q, want 'hello world'", result.Summary)
	}
}

func TestPiProcess_ToolCallEvent(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"tool_execution_start","toolName":"deck_mail_send","args":{"to":"@human","body":"status"}}`,
			`{"type":"tool_execution_end","toolName":"deck_mail_send"}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	// Should get tool_call + output (from agent_end)
	if len(events) < 1 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].Type != "tool_call" {
		t.Errorf("event[0].Type = %q, want tool_call", events[0].Type)
	}
	if events[0].Content != `deck_mail_send: {"to":"@human","body":"status"}` {
		t.Errorf("event[0].Content = %q", events[0].Content)
	}
}

func TestPiProcess_ErrorEvent(t *testing.T) {
	// A failed Pi RPC response emits an error event and makes Wait() return failure.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"response","success":false,"command":"prompt","error":"something went wrong"}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) < 1 {
		t.Fatalf("expected at least 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("event type = %q, want error", events[0].Type)
	}
	if events[0].Content != "command prompt failed: something went wrong" {
		t.Errorf("content = %q", events[0].Content)
	}

	// Wait() should return failure because of the RPC error
	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Success {
		t.Error("expected Wait() result to be failure after RPC error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message in result")
	}
}

func TestPiProcess_ExtensionError_FailsResult(t *testing.T) {
	// An extension_error event should make the final result a failure.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"extension_error","extensionPath":".tack-ext","event":"before_agent_start","error":"hook crashed"}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	// Drain output
	for range proc.Output() {
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Success {
		t.Error("expected Wait() result to be failure after extension error")
	}
}

func TestPiProcess_RPCError_WithoutAgentEnd_FailsResult(t *testing.T) {
	// If Pi rejects the prompt and exits (no agent_end), finalize should
	// still mark the result as failed.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"response","success":false,"command":"prompt","error":"invalid prompt"}`,
			// No agent_end — process exits
		},
	}

	proc := newPiProcess(handle, slog.Default())
	for range proc.Output() {
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Success {
		t.Error("expected Wait() result to be failure after RPC error without agent_end")
	}
}

func TestPiProcess_DeckDoneSignal(t *testing.T) {
	// DECK_DONE: prefix in accumulated text overrides the result summary.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Some output\nDECK_DONE:all tasks completed\nMore text"}}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	for range proc.Output() {
		// drain
	}

	result, _ := proc.Wait()
	if result.Summary != "all tasks completed" {
		t.Errorf("summary = %q, want 'all tasks completed'", result.Summary)
	}
}

func TestPiProcess_SkipInvalidJSON(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			"not json at all",
			"",
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"valid line"}}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	// Should get 1 event: the output from agent_end with accumulated text.
	// Invalid JSON and empty lines are skipped.
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Content != "valid line" {
		t.Errorf("events[0].Content = %q, want 'valid line'", events[0].Content)
	}
}

func TestPiProcess_ProcessExitWithoutAgentEnd(t *testing.T) {
	// Process EOF without agent_end — finalize() provides default result.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial output"}}`,
			// No agent_end — process exits (EOF)
		},
	}

	proc := newPiProcess(handle, slog.Default())
	for range proc.Output() {
		// drain
	}

	result, _ := proc.Wait()
	if !result.Success {
		t.Error("expected success from finalize default")
	}
	if result.Summary != "partial output" {
		t.Errorf("summary = %q, want 'partial output'", result.Summary)
	}
}

func TestPiProcess_Send(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{`{"type":"agent_end"}`},
	}

	proc := newPiProcess(handle, slog.Default())

	err := proc.Send(context.Background(), runtime.AgentMessage{
		Type:    "prompt",
		Content: "do something",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(handle.written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(handle.written))
	}
	if handle.written[0] == "" {
		t.Error("expected non-empty write")
	}
	// Should end with newline (JSONL)
	if handle.written[0][len(handle.written[0])-1] != '\n' {
		t.Error("written data should end with newline")
	}
}

func TestPiProcess_Kill(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{}, // EOF immediately
	}

	proc := newPiProcess(handle, slog.Default())
	// Wait for readLoop to finish
	<-proc.doneCh

	err := proc.Kill()
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !handle.killed {
		t.Error("expected handle to be killed")
	}

	// Second kill should be no-op
	err = proc.Kill()
	if err != nil {
		t.Fatalf("second Kill: %v", err)
	}
}

func TestPiProcess_ReadError(t *testing.T) {
	handle := &errorProcessHandle{err: io.ErrUnexpectedEOF}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("event type = %q, want error", events[0].Type)
	}

	result, _ := proc.Wait()
	if result.Success {
		t.Error("expected failure on read error")
	}
}

func TestPiProcess_TurnEventsNotEmitted(t *testing.T) {
	// Internal lifecycle events (turn_start, turn_end) should not produce output events.
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"turn_start"}`,
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"thinking"}}`,
			`{"type":"turn_end"}`,
			`{"type":"agent_end"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-proc.Output():
			if !ok {
				goto done
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
done:
	// Only the agent_end output event should be emitted
	if len(events) != 1 {
		t.Errorf("expected 1 event (agent_end output), got %d: %+v", len(events), events)
	}
	if len(events) > 0 && events[0].Content != "thinking" {
		t.Errorf("content = %q, want 'thinking'", events[0].Content)
	}
}

// errorProcessHandle always returns an error on ReadLine.
type errorProcessHandle struct {
	err error
}

func (h *errorProcessHandle) Write(_ []byte) error      { return nil }
func (h *errorProcessHandle) ReadLine() (string, error) { return "", h.err }
func (h *errorProcessHandle) Wait() (int, error)        { return -1, h.err }
func (h *errorProcessHandle) Kill() error               { return nil }
