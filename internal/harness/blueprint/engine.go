package blueprint

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Execution represents a running blueprint instance tied to an objective.
type Execution struct {
	ID          string                `json:"id"`
	ProjectID   string                `json:"project_id"`
	BlueprintID string                `json:"blueprint_id"`
	ObjectiveID string                `json:"objective_id"`
	CurrentStep string                `json:"current_step"`
	StepStates  map[string]*StepState `json:"step_states"`
	Status      string                `json:"status"`              // "running", "completed", "failed", "waiting_human", "awaiting_human"
	ParentID    string                `json:"parent_id,omitempty"` // parent execution ID (empty for top-level)
	StreamID    string                `json:"stream_id,omitempty"` // stream this sub-execution drives (empty for top-level)
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

// StepHandler is called by the engine when a step needs to execute.
// The engine itself does NOT implement agent spawning, merging, etc.
// Instead, callers register handlers for each step type.
type StepHandler func(ctx context.Context, exec *Execution, step *Step) (StepResult, error)

// StepResult is the outcome of executing a step handler.
type StepResult struct {
	Status   StepStatus        `json:"status"`
	Error    string            `json:"error,omitempty"`
	Output   string            `json:"output,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Engine drives blueprint execution as a synchronous state machine.
type Engine struct {
	registry *Registry
	handlers map[StepType]StepHandler
	logger   *slog.Logger
}

// NewEngine creates a new blueprint execution engine.
func NewEngine(registry *Registry, logger *slog.Logger) *Engine {
	return &Engine{
		registry: registry,
		handlers: make(map[StepType]StepHandler),
		logger:   logger,
	}
}

// RegisterHandler registers a handler for a step type.
func (e *Engine) RegisterHandler(stepType StepType, handler StepHandler) {
	e.handlers[stepType] = handler
}

// GetBlueprint returns the loaded blueprint by ID.
func (e *Engine) GetBlueprint(id string) (*Blueprint, bool) {
	return e.registry.Get(id)
}

// ResolveDefaultBlueprint returns the default blueprint, or the only loaded blueprint.
func (e *Engine) ResolveDefaultBlueprint() (*Blueprint, error) {
	return e.registry.ResolveDefault()
}

// Start creates a new Execution for the given blueprint and objective,
// initializes all step states to "pending", sets the first step as current,
// and returns the execution. Does NOT advance — call Advance() to begin.
func (e *Engine) Start(ctx context.Context, blueprintID string, objectiveID string) (*Execution, error) {
	bp, ok := e.registry.Get(blueprintID)
	if !ok {
		return nil, fmt.Errorf("blueprint %q not found", blueprintID)
	}

	if len(bp.Steps) == 0 {
		return nil, fmt.Errorf("blueprint %q has no steps", blueprintID)
	}

	now := time.Now()
	exec := &Execution{
		ID:          uuid.New().String(),
		BlueprintID: blueprintID,
		ObjectiveID: objectiveID,
		CurrentStep: bp.Steps[0].ID,
		StepStates:  make(map[string]*StepState, len(bp.Steps)),
		Status:      "running",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	for _, step := range bp.Steps {
		exec.StepStates[step.ID] = &StepState{StepID: step.ID, Status: StepStatusPending}
	}

	e.logger.Info("execution started",
		"execution_id", exec.ID,
		"blueprint_id", blueprintID,
		"objective_id", objectiveID,
	)

	return exec, nil
}

// Advance moves the execution forward by running the current step's handler.
// If the step completes, it advances to the next step (via step.Next).
// If the step fails and has retries remaining, it retries.
// If the step is "human" type, it sets status to "waiting_human" and returns.
// Returns the updated execution.
func (e *Engine) Advance(ctx context.Context, exec *Execution) (*Execution, error) {
	if exec.Status == "completed" || exec.Status == "failed" {
		return exec, fmt.Errorf("execution %s is already %s", exec.ID, exec.Status)
	}
	if exec.Status == "waiting_human" {
		return exec, fmt.Errorf("execution %s is waiting for human approval", exec.ID)
	}

	bp, ok := e.registry.Get(exec.BlueprintID)
	if !ok {
		return exec, fmt.Errorf("blueprint %q not found", exec.BlueprintID)
	}

	step, err := e.GetStepByID(bp, exec.CurrentStep)
	if err != nil {
		return exec, fmt.Errorf("advancing execution: %w", err)
	}

	if step.Type == StepTypeHuman {
		state := exec.StepStates[step.ID]
		state.Status = StepStatusBlocked
		exec.Status = "waiting_human"
		exec.UpdatedAt = time.Now()
		e.logger.Info("execution waiting for human approval", "execution_id", exec.ID, "step", step.ID)
		return exec, nil
	}

	handler, ok := e.handlers[step.Type]
	if !ok {
		return exec, fmt.Errorf("no handler registered for step type %q", step.Type)
	}

	state := exec.StepStates[step.ID]
	state.Status = StepStatusRunning
	exec.UpdatedAt = time.Now()
	result, err := handler(ctx, exec, step)
	if err != nil {
		result = StepResult{Status: StepStatusFailed, Error: err.Error()}
	}

	switch result.Status {
	case StepStatusCompleted:
		state.Status = StepStatusCompleted
		state.Error = ""
		state.Output = result.Output
		state.Metadata = cloneMetadata(result.Metadata)
		return e.advanceToNext(exec, step)

	case StepStatusFailed:
		state.Error = result.Error
		state.Output = result.Output
		state.Metadata = cloneMetadata(result.Metadata)
		if state.RetryCount < step.Retry {
			state.RetryCount++
			state.Status = StepStatusPending
			exec.UpdatedAt = time.Now()
			return exec, nil
		}

		if step.OnFail != "" {
			maxIter := step.MaxFixIterations
			if maxIter <= 0 {
				maxIter = 3
			}
			if state.FixIterations < maxIter {
				targetState := exec.StepStates[step.OnFail]
				if targetState == nil {
					state.Status = StepStatusFailed
					exec.Status = "failed"
					exec.UpdatedAt = time.Now()
					return exec, nil
				}
				state.FixIterations++
				state.RetryCount = 0
				state.Status = StepStatusPending
				targetState.Status = StepStatusPending
				targetState.Error = ""
				targetState.Metadata = map[string]string{"fix_context": result.Error}
				exec.CurrentStep = step.OnFail
				exec.UpdatedAt = time.Now()
				return exec, nil
			}
		}

		state.Status = StepStatusFailed
		if step.Optional {
			state.Status = StepStatusSkipped
			return e.advanceToNext(exec, step)
		}
		exec.Status = "failed"
		exec.UpdatedAt = time.Now()
		return exec, nil

	default:
		state.Status = result.Status
		state.Output = result.Output
		state.Metadata = cloneMetadata(result.Metadata)
		exec.UpdatedAt = time.Now()
		return exec, nil
	}
}

// advanceToNext moves the execution to the next step, or marks it completed.
func (e *Engine) advanceToNext(exec *Execution, step *Step) (*Execution, error) {
	if step.Next == "" {
		exec.Status = "completed"
		exec.CurrentStep = ""
		exec.UpdatedAt = time.Now()
		e.logger.Info("execution completed", "execution_id", exec.ID)
		return exec, nil
	}

	exec.CurrentStep = step.Next
	exec.UpdatedAt = time.Now()
	e.logger.Info("advanced to step", "execution_id", exec.ID, "step", step.Next)
	return exec, nil
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ApproveHuman unblocks a "waiting_human" execution and advances to the next step.
func (e *Engine) ApproveHuman(ctx context.Context, exec *Execution) (*Execution, error) {
	if exec.Status != "waiting_human" {
		return exec, fmt.Errorf("execution %s is not waiting for human approval (status: %s)", exec.ID, exec.Status)
	}

	bp, ok := e.registry.Get(exec.BlueprintID)
	if !ok {
		return exec, fmt.Errorf("blueprint %q not found", exec.BlueprintID)
	}

	step, err := e.GetStepByID(bp, exec.CurrentStep)
	if err != nil {
		return exec, fmt.Errorf("approving human step: %w", err)
	}

	state := exec.StepStates[step.ID]
	state.Status = StepStatusCompleted
	exec.Status = "running"
	exec.UpdatedAt = time.Now()
	return e.advanceToNext(exec, step)
}

// GetStepByID returns the step definition from the blueprint.
func (e *Engine) GetStepByID(bp *Blueprint, stepID string) (*Step, error) {
	for i := range bp.Steps {
		if bp.Steps[i].ID == stepID {
			return &bp.Steps[i], nil
		}
	}
	return nil, fmt.Errorf("step %q not found in blueprint %q", stepID, bp.ID)
}
