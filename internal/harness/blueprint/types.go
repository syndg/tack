package blueprint

import "github.com/syndg/deck/internal/harness/tools"

// StepType identifies the kind of step in a blueprint.
type StepType string

const (
	StepTypeAgent         StepType = "agent"
	StepTypeDeterministic StepType = "deterministic"
	StepTypeHuman         StepType = "human"
	StepTypeBlueprintRef  StepType = "blueprint_ref"
)

// CommitMode controls how agent changes are committed after a step completes.
type CommitMode string

const (
	CommitModeAuto  CommitMode = "auto"  // deterministic commit after agent finishes
	CommitModeAgent CommitMode = "agent" // agent is instructed to commit itself
	CommitModeNone  CommitMode = "none"  // no commit (scout/analysis agents)
)

// Blueprint defines a workflow as a sequence of steps.
type Blueprint struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Trigger     string `yaml:"trigger" json:"trigger"`
	Steps       []Step `yaml:"steps" json:"steps"`
}

// MessageRequests controls which delivery messages an agent should generate.
type MessageRequests struct {
	Commit bool `yaml:"commit,omitempty" json:"commit,omitempty"`
	PR     bool `yaml:"pr,omitempty" json:"pr,omitempty"`
}

// Any reports whether at least one message has been requested.
func (m *MessageRequests) Any() bool {
	return m != nil && (m.Commit || m.PR)
}

// EscalationConfig controls how failed streams are escalated to humans.
type EscalationConfig struct {
	Context             string `yaml:"context,omitempty" json:"context,omitempty"`               // "full" | "minimal" | "error_only" (default: "full")
	IncludeAgentHistory bool   `yaml:"include_agent_history,omitempty" json:"include_agent_history,omitempty"`
	Channel             string `yaml:"channel,omitempty" json:"channel,omitempty"`               // "default" | future: "discord", "slack"
	Prompt              string `yaml:"prompt,omitempty" json:"prompt,omitempty"`                 // custom question for human
}

// EffectiveContext returns the context level, defaulting to "full".
func (e *EscalationConfig) EffectiveContext() string {
	if e == nil || e.Context == "" {
		return "full"
	}
	return e.Context
}

// EffectivePrompt returns the prompt, defaulting to a standard question.
func (e *EscalationConfig) EffectivePrompt() string {
	if e == nil || e.Prompt == "" {
		return "How should the agent resolve this?"
	}
	return e.Prompt
}

// Step is a single node in a blueprint workflow.
type Step struct {
	ID               string            `yaml:"id" json:"id"`
	Type             StepType          `yaml:"type" json:"type"`
	Role             string            `yaml:"role,omitempty" json:"role,omitempty"`
	Action           string            `yaml:"action,omitempty" json:"action,omitempty"`
	Ref              string            `yaml:"ref,omitempty" json:"ref,omitempty"`
	Description      string            `yaml:"description,omitempty" json:"description,omitempty"`
	Next             string            `yaml:"next,omitempty" json:"next,omitempty"`
	Retry            int               `yaml:"retry,omitempty" json:"retry,omitempty"`
	Optional         bool              `yaml:"optional,omitempty" json:"optional,omitempty"`
	Tools            *tools.ToolScope  `yaml:"tools,omitempty" json:"tools,omitempty"`
	Commit           CommitMode        `yaml:"commit,omitempty" json:"commit,omitempty"`
	Messages         *MessageRequests  `yaml:"messages,omitempty" json:"messages,omitempty"`
	MessageSource    string            `yaml:"message_source,omitempty" json:"message_source,omitempty"`
	OnFail           string            `yaml:"on_fail,omitempty" json:"on_fail,omitempty"`
	MaxFixIterations int               `yaml:"max_fix_iterations,omitempty" json:"max_fix_iterations,omitempty"`
	OnStreamFailure  string            `yaml:"on_stream_failure,omitempty" json:"on_stream_failure,omitempty"` // "escalate" | "fail" (blueprint_ref only)
	Escalation       *EscalationConfig `yaml:"escalation,omitempty" json:"escalation,omitempty"`              // blueprint_ref only
}

// EffectiveCommitMode returns the commit mode for this step, defaulting to
// CommitModeAuto for agent steps and CommitModeNone for all others.
func (s *Step) EffectiveCommitMode() CommitMode {
	if s.Commit != "" {
		return s.Commit
	}
	if s.Type == StepTypeAgent {
		return CommitModeAuto
	}
	return CommitModeNone
}

// StepStatus tracks the lifecycle state of a step during execution.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
	StepStatusBlocked   StepStatus = "blocked"
)

// StepState tracks runtime state for a step within an execution.
type StepState struct {
	StepID         string            `json:"step_id"`
	Status         StepStatus        `json:"status"`
	RetryCount     int               `json:"retry_count"`
	FixIterations  int               `json:"fix_iterations,omitempty"`
	Error          string            `json:"error,omitempty"`
	Output         string            `json:"output,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}
