package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/harness/gates"
	"github.com/syndg/deck/internal/sandbox"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
	"github.com/syndg/deck/internal/services/merge"
)

// Handlers implements blueprint step handlers for deterministic and human steps.
type Handlers struct {
	scheduler       *Scheduler
	gateRunner      *gates.Runner
	lifecycle       *lifecycle.Manager
	mergeProcessor  *merge.Processor
	plans           *db.PlanStore
	streams         *db.StreamStore
	objectives      *db.ObjectiveStore
	executions      *db.ExecutionStore
	agents          *db.AgentStore
	sandboxProvider sandbox.SandboxProvider
	eventBus        *events.PersistentBus
	logger          *slog.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(
	scheduler *Scheduler,
	gateRunner *gates.Runner,
	lc *lifecycle.Manager,
	mergeProcessor *merge.Processor,
	plans *db.PlanStore,
	streams *db.StreamStore,
	objectives *db.ObjectiveStore,
	executions *db.ExecutionStore,
	agents *db.AgentStore,
	sandboxProvider sandbox.SandboxProvider,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Handlers {
	return &Handlers{
		scheduler:       scheduler,
		gateRunner:      gateRunner,
		lifecycle:       lc,
		mergeProcessor:  mergeProcessor,
		plans:           plans,
		streams:         streams,
		objectives:      objectives,
		executions:      executions,
		agents:          agents,
		sandboxProvider: sandboxProvider,
		eventBus:        eventBus,
		logger:          logger,
	}
}

// HandleDeterministic routes to the correct handler based on step.Action.
// This function is registered with the blueprint engine as the StepTypeDeterministic handler.
// Actions:
//   - "dispatch_streams"   → dispatches lead agents for ready streams
//   - "run_quality_gates"  → runs quality gates in sandbox
//   - "signal_merge_ready" → marks streams as merge-ready and publishes EventMergeQueued
//   - "mark_complete"      → transitions objective to reviewing
//   - "merge_queue"        → enqueues merge_ready streams into the merge processor
//   - "create_pr"          → pushes branch and creates a GitHub PR
func (h *Handlers) HandleDeterministic(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	switch step.Action {
	case "dispatch_streams":
		return h.dispatchStreams(ctx, exec)
	case "run_quality_gates":
		return h.runQualityGates(ctx, exec)
	case "signal_merge_ready":
		return h.signalMergeReady(ctx, exec)
	case "mark_complete":
		return h.markComplete(ctx, exec)
	case "merge_queue":
		return h.mergeQueue(ctx, exec)
	case "create_pr":
		return h.createPR(ctx, exec)
	default:
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("unknown deterministic action: %q", step.Action),
		}, nil
	}
}

// HandleHuman pauses execution until human approval.
// Returns StepResult with status "waiting_human" (mapped to StepStatusBlocked).
// The blueprint engine's Advance() handles the rest — it detects human steps,
// sets execution status to "waiting_human", and stops advancing.
// Execution resumes when ApproveHuman() is called via the API.
func (h *Handlers) HandleHuman(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	h.logger.Info("execution paused waiting for human approval",
		"execution_id", exec.ID,
		"step_id", step.ID,
		"objective_id", exec.ObjectiveID,
	)
	return blueprint.StepResult{
		Status: blueprint.StepStatusBlocked,
		Output: "waiting for human approval",
	}, nil
}

// dispatchStreams implements the "dispatch_streams" deterministic action.
// It validates the plan and logs the set of currently ready streams. Actual
// claiming/spawning is owned by the blueprint_ref handler so the execution
// loop doesn't race with the global event loop over initial stream dispatch.
func (h *Handlers) dispatchStreams(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	ready, err := h.scheduler.GetReadyStreams(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting ready streams: %s", err),
		}, nil
	}

	h.logger.Info("dispatching streams",
		"execution_id", exec.ID,
		"objective_id", exec.ObjectiveID,
		"plan_id", plan.ID,
		"ready_count", len(ready),
	)

	for _, stream := range ready {
		h.logger.Info("stream ready for execution",
			"stream_id", stream.ID,
			"stream_title", stream.Title,
		)
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// runQualityGates implements the "run_quality_gates" deterministic action.
// Gets quality gates from the plan, finds an active lead agent sandbox for the
// objective, and runs the gates sequentially. Returns failed if any gate fails.
func (h *Handlers) runQualityGates(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	if len(plan.QualityGates) == 0 {
		h.logger.Info("no quality gates configured, skipping",
			"execution_id", exec.ID,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	// Convert quality gate commands to gates.Gate structs.
	gateList := make([]gates.Gate, len(plan.QualityGates))
	for i, cmd := range plan.QualityGates {
		gateList[i] = gates.Gate{
			Name:    fmt.Sprintf("gate-%d", i+1),
			Command: cmd,
		}
	}

	// Prefer a lead agent sandbox for the objective, but fall back to any agent
	// sandbox. Hotfix/single-agent flows only have a builder sandbox.
	sessions, err := h.agents.ListByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing agents for objective: %s", err),
		}, nil
	}

	findSandbox := func(preferredRole domain.AgentRole) sandbox.Sandbox {
		for _, session := range sessions {
			if session.SandboxID == "" {
				continue
			}
			if preferredRole != "" && session.Role != preferredRole {
				continue
			}
			found, err := h.sandboxProvider.Get(ctx, session.SandboxID)
			if err != nil {
				h.logger.Warn("could not retrieve sandbox for agent",
					"sandbox_id", session.SandboxID,
					"session_id", session.ID,
					"role", session.Role,
					"error", err,
				)
				continue
			}
			return found
		}
		return nil
	}

	var sb sandbox.Sandbox
	sb = findSandbox(domain.AgentRoleLead)
	if sb == nil {
		sb = findSandbox("")
	}
	if sb == nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  "no agent sandbox found for quality gate execution",
		}, nil
	}

	h.logger.Info("running quality gates",
		"execution_id", exec.ID,
		"objective_id", exec.ObjectiveID,
		"gate_count", len(gateList),
		"sandbox_id", sb.ID(),
	)

	result, err := h.gateRunner.Run(ctx, sb, gateList, false)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("running quality gates: %s", err),
		}, nil
	}

	if !result.AllPassed {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  "one or more quality gates failed",
		}, nil
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// signalMergeReady implements the "signal_merge_ready" deterministic action.
// Updates all completed streams for the objective's plan to "merge_ready" status
// and publishes EventMergeQueued for each.
func (h *Handlers) signalMergeReady(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	streamList, err := h.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing streams for plan: %s", err),
		}, nil
	}

	for _, stream := range streamList {
		if stream.Status != "completed" {
			continue
		}

		if err := h.streams.UpdateStatus(ctx, stream.ID, "merge_ready"); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("updating stream %s to merge_ready: %s", stream.ID, err),
			}, nil
		}

		payload, _ := json.Marshal(map[string]string{
			"stream_id":    stream.ID,
			"plan_id":      plan.ID,
			"objective_id": exec.ObjectiveID,
		})
		h.eventBus.Publish(domain.Event{
			Type:      domain.EventMergeQueued,
			Objective: exec.ObjectiveID,
			Stream:    stream.ID,
			Payload:   string(payload),
			CreatedAt: time.Now(),
		})

		h.logger.Info("stream signaled for merge",
			"stream_id", stream.ID,
			"stream_title", stream.Title,
		)
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// markComplete implements the "mark_complete" deterministic action.
// Transitions the objective to "reviewing" (human sign-off required by default).
// Autonomy-based direct completion (→ "completed") is deferred to a later phase.
func (h *Handlers) markComplete(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	if err := h.lifecycle.Transition(ctx, exec.ObjectiveID, domain.ObjectiveStatusReviewing); err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("transitioning objective to reviewing: %s", err),
		}, nil
	}

	h.logger.Info("objective marked for review",
		"execution_id", exec.ID,
		"objective_id", exec.ObjectiveID,
	)

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// createPR implements the "create_pr" deterministic action.
// Pushes the agent's branch to origin and creates a GitHub PR via `gh pr create`.
func (h *Handlers) createPR(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	obj, err := h.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	sb, err := h.findSandboxForObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  err.Error(),
		}, nil
	}

	// Get the current branch name.
	branchResult, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil || branchResult.ExitCode != 0 {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting branch name: %s", branchResult.Stderr),
		}, nil
	}
	branch := strings.TrimSpace(branchResult.Stdout)

	// Push the branch to origin.
	pushResult, err := sb.Exec(ctx, fmt.Sprintf("git push -u origin %s", branch), sandbox.ExecOpts{})
	if err != nil || pushResult.ExitCode != 0 {
		stderr := ""
		if pushResult.Stderr != "" {
			stderr = pushResult.Stderr
		}
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("pushing branch: %s", stderr),
		}, nil
	}

	h.logger.Info("branch pushed",
		"branch", branch,
		"objective_id", exec.ObjectiveID,
	)

	// Create PR via gh CLI.
	title := obj.Description
	body := fmt.Sprintf("Automated PR created by Deck.\n\nObjective: %s\nObjective ID: %s", obj.Description, obj.ID)
	escapedTitle := "'" + escapeShellSingleQuote(title) + "'"
	escapedBody := "'" + escapeShellSingleQuote(body) + "'"
	prCmd := fmt.Sprintf("gh pr create --title %s --body %s --head %s", escapedTitle, escapedBody, branch)

	prResult, err := sb.Exec(ctx, prCmd, sandbox.ExecOpts{})
	if err != nil || prResult.ExitCode != 0 {
		stderr := ""
		if prResult.Stderr != "" {
			stderr = prResult.Stderr
		}
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("creating PR: %s", stderr),
		}, nil
	}

	prURL := prResult.Stdout
	h.logger.Info("PR created",
		"url", prURL,
		"branch", branch,
		"objective_id", exec.ObjectiveID,
	)

	return blueprint.StepResult{
		Status: blueprint.StepStatusCompleted,
		Output: prURL,
	}, nil
}

// findSandboxForObjective locates an active agent sandbox for the given objective.
// Prefers a lead agent sandbox; falls back to any agent sandbox (e.g. builder in hotfix).
func (h *Handlers) findSandboxForObjective(ctx context.Context, objectiveID string) (sandbox.Sandbox, error) {
	sessions, err := h.agents.ListByObjective(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("listing agents for objective: %s", err)
	}

	find := func(preferredRole domain.AgentRole) sandbox.Sandbox {
		for _, session := range sessions {
			if session.SandboxID == "" {
				continue
			}
			if preferredRole != "" && session.Role != preferredRole {
				continue
			}
			found, err := h.sandboxProvider.Get(ctx, session.SandboxID)
			if err != nil {
				continue
			}
			return found
		}
		return nil
	}

	sb := find(domain.AgentRoleLead)
	if sb == nil {
		sb = find("")
	}
	if sb == nil {
		return nil, fmt.Errorf("no agent sandbox found for objective %s", objectiveID)
	}
	return sb, nil
}

func escapeShellSingleQuote(s string) string {
	return strings.Replace(s, "'", `'\''`, -1)
}

// mergeQueue implements the "merge_queue" deterministic action.
// Enqueues all merge_ready streams for the objective into the merge processor.
// The merge processor runs asynchronously — this handler completes immediately
// and the processor transitions the objective to "reviewing" when all merges finish.
func (h *Handlers) mergeQueue(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	if h.mergeProcessor == nil {
		h.logger.Warn("merge processor not configured, skipping merge queue",
			"execution_id", exec.ID,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	// Get the plan for this objective.
	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{Status: blueprint.StepStatusFailed}, fmt.Errorf("getting plan: %w", err)
	}

	// Get all streams for the plan.
	streams, err := h.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{Status: blueprint.StepStatusFailed}, fmt.Errorf("listing streams: %w", err)
	}

	// Enqueue all merge_ready streams.
	for _, stream := range streams {
		if stream.Status == domain.StreamStatusMergeReady {
			if err := h.mergeProcessor.EnqueueStream(ctx, stream.ID); err != nil {
				h.logger.Error("failed to enqueue stream for merge",
					"stream_id", stream.ID,
					"error", err,
				)
			}
		}
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}
