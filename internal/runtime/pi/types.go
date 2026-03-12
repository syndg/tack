package pi

// Pi RPC protocol types for bidirectional communication with Pi agents.

// PiCommand represents a command sent to Pi via stdin (JSONL).
type PiCommand struct {
	Type    string `json:"type"`              // "prompt", "tool_result", "cancel"
	Content string `json:"content,omitempty"` // prompt text or tool result
	ID      string `json:"id,omitempty"`      // correlation ID for tool results
}

// PiEvent represents an event received from Pi via stdout (JSONL).
type PiEvent struct {
	Type    string `json:"type"`              // "output", "tool_call", "error", "done", "status"
	Content string `json:"content,omitempty"` // text output or error message
	Tool    string `json:"tool,omitempty"`    // tool name (for tool_call events)
	Args    string `json:"args,omitempty"`    // JSON-encoded tool arguments
	ID      string `json:"id,omitempty"`      // event correlation ID
	Success bool   `json:"success,omitempty"` // final status (for done events)
	Summary string `json:"summary,omitempty"` // completion summary (for done events)
}

// Pi event type constants.
const (
	PiEventOutput   = "output"
	PiEventToolCall = "tool_call"
	PiEventError    = "error"
	PiEventDone     = "done"
	PiEventStatus   = "status"
)

// Pi command type constants.
const (
	PiCommandPrompt     = "prompt"
	PiCommandToolResult = "tool_result"
	PiCommandCancel     = "cancel"
)
