package pi

import "encoding/json"

// Pi RPC protocol types for bidirectional communication with Pi agents.

// PiCommand represents a command sent to Pi via stdin (JSONL).
type PiCommand struct {
	Type    string `json:"type"`              // "prompt", "steer", "abort"
	Message string `json:"message,omitempty"` // prompt text (Pi expects "message", not "content")
	ID      string `json:"id,omitempty"`      // correlation ID
}

// PiEvent represents an event received from Pi via stdout (JSONL).
// Pi emits many event types; we only care about a subset.
type PiEvent struct {
	Type string `json:"type"` // "response", "agent_start", "agent_end", "message_update", "tool_execution_start", "tool_execution_end", "extension_error", etc.

	// Response fields (type: "response")
	Command string `json:"command,omitempty"` // which command this responds to
	Success bool   `json:"success,omitempty"` // response success
	Error   string `json:"error,omitempty"`   // error message

	// Message fields (type: "message_start", "message_update", "message_end")
	Message              json.RawMessage `json:"message,omitempty"`
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent,omitempty"`

	// Agent end fields (type: "agent_end")
	Messages json.RawMessage `json:"messages,omitempty"`

	// Tool execution fields (type: "tool_execution_start", "tool_execution_end")
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	IsError    bool            `json:"isError,omitempty"`

	// Extension error fields (type: "extension_error")
	ExtensionPath string `json:"extensionPath,omitempty"`
	Event         string `json:"event,omitempty"` // which hook failed

	// Auto-compaction fields
	Reason  string `json:"reason,omitempty"`
	Aborted bool   `json:"aborted,omitempty"`
}

// AssistantMessageEvent contains delta info from Pi's streaming.
type AssistantMessageEvent struct {
	Type  string `json:"type"`            // "text_delta", "toolcall_delta", "thinking_delta", etc.
	Delta string `json:"delta,omitempty"` // text chunk
}

// AgentMessage from Pi's message format.
type AgentMessageInfo struct {
	Role    string          `json:"role"`              // "user", "assistant", "toolResult"
	Content json.RawMessage `json:"content,omitempty"` // string or content blocks
}

// Pi event type constants.
const (
	PiEventResponse            = "response"
	PiEventAgentStart          = "agent_start"
	PiEventAgentEnd            = "agent_end"
	PiEventMessageStart        = "message_start"
	PiEventMessageUpdate       = "message_update"
	PiEventMessageEnd          = "message_end"
	PiEventToolExecStart       = "tool_execution_start"
	PiEventToolExecUpdate      = "tool_execution_update"
	PiEventToolExecEnd         = "tool_execution_end"
	PiEventExtensionError      = "extension_error"
	PiEventAutoCompactionStart = "auto_compaction_start"
	PiEventAutoCompactionEnd   = "auto_compaction_end"
	PiEventAutoRetryStart      = "auto_retry_start"
	PiEventAutoRetryEnd        = "auto_retry_end"
	PiEventTurnStart           = "turn_start"
	PiEventTurnEnd             = "turn_end"
)

// Pi command type constants.
const (
	PiCommandPrompt = "prompt"
	PiCommandSteer  = "steer"
	PiCommandAbort  = "abort"
)
