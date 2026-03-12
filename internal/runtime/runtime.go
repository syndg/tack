package runtime

import (
	"context"

	"github.com/syndg/deck/internal/sandbox"
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
	Type    string `json:"type"` // "output", "tool_call", "error"
	Content string `json:"content"`
}

type AgentResult struct {
	Success bool   `json:"success"`
	Summary string `json:"summary"`
	Error   string `json:"error"`
}
