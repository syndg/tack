package runtime

import (
	"context"

	"github.com/syndg/tack/internal/sandbox"
)

type AgentRuntime interface {
	Spawn(ctx context.Context, sb sandbox.Sandbox, opts AgentOpts) (AgentProcess, error)
	Name() string
	SupportsRPC() bool
	SupportsHooks() bool
}

type AgentProcess interface {
	Send(ctx context.Context, msg AgentMessage) error
	Output() <-chan AgentEvent
	Wait() (AgentResult, error)
	Kill() error
}

type AgentOpts struct {
	Role    string            `json:"role"`
	Overlay string            `json:"overlay"`
	Tools   []string          `json:"tools"`
	Rules   []string          `json:"rules"`
	Model   string            `json:"model"`
	EnvVars map[string]string `json:"env_vars"`
	WorkDir string            `json:"work_dir"`
}

type AgentMessage struct {
	Type    string `json:"type"` // "prompt", "steer"
	Content string `json:"content"`
}

type AgentEvent struct {
	Type    string `json:"type"` // "output", "tool_call", "tool_end", "error"
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

type AgentResult struct {
	Success bool   `json:"success"`
	Summary string `json:"summary"`
	Error   string `json:"error"`
}
