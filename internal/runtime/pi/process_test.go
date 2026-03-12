package pi

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/syndg/deck/internal/runtime"
)

func TestPiProcess_OutputEvent(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"output","content":"hello world"}`,
			`{"type":"done","success":true,"summary":"completed"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "output" || events[0].Content != "hello world" {
		t.Errorf("event[0] = %+v, want output/hello world", events[0])
	}
	if events[1].Type != "output" || events[1].Content != "completed" {
		t.Errorf("event[1] = %+v, want output/completed", events[1])
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %+v", result)
	}
	if result.Summary != "completed" {
		t.Errorf("summary = %q, want completed", result.Summary)
	}
}

func TestPiProcess_ToolCallEvent(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"tool_call","tool":"deck_mail_send","args":"{\"to\":\"@human\",\"content\":\"status\"}"}`,
			`{"type":"done","success":true,"summary":"done"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) < 1 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].Type != "tool_call" {
		t.Errorf("event[0].Type = %q, want tool_call", events[0].Type)
	}
	if events[0].Content != `deck_mail_send: {"to":"@human","content":"status"}` {
		t.Errorf("event[0].Content = %q", events[0].Content)
	}
}

func TestPiProcess_ErrorEvent(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"error","content":"something went wrong"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("event type = %q, want error", events[0].Type)
	}
	if events[0].Content != "something went wrong" {
		t.Errorf("content = %q", events[0].Content)
	}
}

func TestPiProcess_DeckDoneSignal(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"done","success":true,"content":"DECK_DONE:all tasks completed","summary":"partial"}`,
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
			`{"type":"output","content":"valid line"}`,
			`{"type":"done","success":true,"summary":"ok"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	var events []runtime.AgentEvent
	for ev := range proc.Output() {
		events = append(events, ev)
	}

	// Should only get 2 events (output + done summary), skipping invalid lines
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Content != "valid line" {
		t.Errorf("events[0].Content = %q, want 'valid line'", events[0].Content)
	}
}

func TestPiProcess_DoneFailure(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"done","success":false,"content":"task failed","summary":"error occurred"}`,
		},
	}

	proc := newPiProcess(handle, slog.Default())
	for range proc.Output() {
		// drain
	}

	result, _ := proc.Wait()
	if result.Success {
		t.Error("expected failure")
	}
	if result.Error != "task failed" {
		t.Errorf("error = %q, want 'task failed'", result.Error)
	}
}

func TestPiProcess_Send(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{`{"type":"done","success":true,"summary":"ok"}`},
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

func TestPiProcess_StatusEvent(t *testing.T) {
	handle := &mockProcessHandle{
		lines: []string{
			`{"type":"status","content":"thinking..."}`,
			`{"type":"done","success":true,"summary":"ok"}`,
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
	// Status events are logged, not emitted — only done's summary is emitted
	if len(events) != 1 {
		t.Errorf("expected 1 event (done output), got %d: %+v", len(events), events)
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
