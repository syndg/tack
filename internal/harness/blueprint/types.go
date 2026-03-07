package blueprint

// StepType identifies the kind of step in a blueprint.
type StepType string

const (
	StepTypeAgent         StepType = "agent"
	StepTypeDeterministic StepType = "deterministic"
	StepTypeHuman         StepType = "human"
	StepTypeBlueprintRef  StepType = "blueprint_ref"
)

// Blueprint defines a workflow as a sequence of steps.
type Blueprint struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Trigger     string `yaml:"trigger" json:"trigger"`
	Steps       []Step `yaml:"steps" json:"steps"`
}

// Step is a single node in a blueprint workflow.
type Step struct {
	ID          string     `yaml:"id" json:"id"`
	Type        StepType   `yaml:"type" json:"type"`
	Role        string     `yaml:"role,omitempty" json:"role,omitempty"`
	Action      string     `yaml:"action,omitempty" json:"action,omitempty"`
	Ref         string     `yaml:"ref,omitempty" json:"ref,omitempty"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Next        string     `yaml:"next,omitempty" json:"next,omitempty"`
	Retry       int        `yaml:"retry,omitempty" json:"retry,omitempty"`
	Optional    bool       `yaml:"optional,omitempty" json:"optional,omitempty"`
	Tools       *ToolScope `yaml:"tools,omitempty" json:"tools,omitempty"`
}

// ToolScope restricts which tools are available during a step.
type ToolScope struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
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
	StepID     string     `json:"step_id"`
	Status     StepStatus `json:"status"`
	RetryCount int        `json:"retry_count"`
	Error      string     `json:"error,omitempty"`
}
