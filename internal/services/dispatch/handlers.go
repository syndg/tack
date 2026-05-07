package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/gates"
	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/observability"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/services/agents"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// MergeHelper is the merge-processor surface used by step handlers.
// Extracted as an interface so the dispatch package does not depend on
// the merge package directly, allowing runs to own construction.
type MergeHelper interface {
	EnqueueStream(ctx context.Context, streamID string) error
	MergerSandboxID(objectiveID string) string
	ResetMergingEntries(ctx context.Context, streamID string)
}

// Handlers implements blueprint step handlers for deterministic and human steps.
type Handlers struct {
	scheduler       *Scheduler
	gateRunner      *gates.Runner
	lifecycle       *lifecycle.Manager
	mergeProcessor  MergeHelper
	engine          *blueprint.Engine
	agentRuntime    runtime.AgentRuntime
	plans           *db.PlanStore
	streams         *db.StreamStore
	objectives      *db.ObjectiveStore
	executions      *db.ExecutionStore
	agents          *db.AgentStore
	attempts        *db.AttemptStore
	sandboxProvider sandbox.SandboxProvider
	eventBus        *events.PersistentBus
	obs             *observability.Recorder
	baseBranch      string
	creds           *credentials.Store
	runtimeAuth     config.RuntimeAuthConfig
	defaultModel    string
	githubAPIBase   string
	logger          *slog.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(
	scheduler *Scheduler,
	gateRunner *gates.Runner,
	lc *lifecycle.Manager,
	mergeProcessor MergeHelper,
	engine *blueprint.Engine,
	agentRuntime runtime.AgentRuntime,
	plans *db.PlanStore,
	streams *db.StreamStore,
	objectives *db.ObjectiveStore,
	executions *db.ExecutionStore,
	agents *db.AgentStore,
	attempts *db.AttemptStore,
	sandboxProvider sandbox.SandboxProvider,
	eventBus *events.PersistentBus,
	obs *observability.Recorder,
	baseBranch string,
	creds *credentials.Store,
	runtimeAuth config.RuntimeAuthConfig,
	defaultModel string,
	logger *slog.Logger,
) *Handlers {
	if baseBranch == "" {
		baseBranch = config.DefaultBaseBranch
	}
	return &Handlers{
		scheduler:       scheduler,
		gateRunner:      gateRunner,
		lifecycle:       lc,
		mergeProcessor:  mergeProcessor,
		engine:          engine,
		agentRuntime:    agentRuntime,
		plans:           plans,
		streams:         streams,
		objectives:      objectives,
		executions:      executions,
		agents:          agents,
		attempts:        attempts,
		sandboxProvider: sandboxProvider,
		eventBus:        eventBus,
		obs:             obs,
		baseBranch:      baseBranch,
		creds:           creds,
		runtimeAuth:     runtimeAuth,
		defaultModel:    defaultModel,
		githubAPIBase:   "https://api.github.com",
		logger:          logger,
	}
}

// HandleDeterministic routes to the correct handler based on step.Action.
// This function is registered with the blueprint engine as the StepTypeDeterministic handler.
// Actions:
//   - "dispatch_streams"   → dispatches lead agents for ready streams
//   - "run_quality_gates"  → runs quality gates in sandbox
//   - "signal_merge_ready" → marks streams as merge-ready and publishes EventMergeQueued
//   - "mark_stream_merged" → marks a sub-execution stream as merged without merge queue
//   - "mark_complete"      → transitions objective to completed
//   - "merge_queue"        → enqueues merge_ready streams and blocks until all resolve
//   - "create_pr"          → pushes branch and creates a GitHub PR
func (h *Handlers) HandleDeterministic(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	switch step.Action {
	case "dispatch_streams":
		return h.dispatchStreams(ctx, exec)
	case "run_quality_gates":
		return h.runQualityGates(ctx, exec, step)
	case "signal_merge_ready":
		return h.signalMergeReady(ctx, exec)
	case "mark_stream_merged":
		return h.markStreamMerged(ctx, exec)
	case "mark_complete":
		return h.markComplete(ctx, exec)
	case "merge_queue":
		return h.mergeQueue(ctx, exec)
	case "create_pr":
		return h.createPR(ctx, exec, step)
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
func (h *Handlers) runQualityGates(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
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

	// Find the right sandbox for quality gates:
	// - Sub-execution (StreamID set): use the builder sandbox for THIS stream
	// - Top-level: prefer lead, then builder, then any
	sessions, err := h.agents.ListByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing agents for objective: %s", err),
		}, nil
	}

	findSandbox := func(streamID string, preferredRole domain.AgentRole) sandbox.Sandbox {
		for _, session := range sessions {
			if session.SandboxID == "" {
				continue
			}
			if streamID != "" && session.StreamID != streamID {
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

	var sb sandbox.Sandbox
	if exec.StreamID != "" {
		// Sub-execution: prefer builder for this stream, fall back to any for this stream.
		sb = findSandbox(exec.StreamID, domain.AgentRoleBuilder)
		if sb == nil {
			sb = findSandbox(exec.StreamID, "")
		}
	} else {
		// Top-level: prefer lead, then builder, then any.
		sb = findSandbox("", domain.AgentRoleLead)
		if sb == nil {
			sb = findSandbox("", domain.AgentRoleBuilder)
		}
		if sb == nil {
			sb = findSandbox("", "")
		}
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
		// File-attribution: in a sub-execution, only fail if errors are in
		// the stream's file scope. Errors in other streams' files are caught
		// at merge time by the post-merge quality gates.
		failureSummary := formatGateErrors(result)
		if exec.StreamID != "" {
			stream, streamErr := h.streams.Get(ctx, exec.StreamID)
			if streamErr == nil && len(stream.FileScope) > 0 {
				if !gateErrorsInScope(result, stream.FileScope) {
					h.logger.Info("gate failed but no errors in stream file scope, passing",
						"stream_id", exec.StreamID,
						"execution_id", exec.ID,
					)
					return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
				}
				// Only include in-scope errors in the fix context
				failureSummary = formatGateErrorsScoped(result, stream.FileScope)
			}
		}
		humanGuidance := humanGuidanceFromRetryContext(exec, step.OnFail)
		retryCfg := resolveExecutionRetryConfig(h.engine, exec)
		policy := resolveStepRetryOverride(step)
		decisionError := failureSummary
		attempt, decision := (&Coordinator{attempts: h.attempts, eventBus: h.eventBus, logger: h.logger}).recordRecoveryAttempt(ctx, recoveryAttemptInput{
			ObjectiveID:   exec.ObjectiveID,
			ExecutionID:   exec.ID,
			StreamID:      exec.StreamID,
			StepID:        step.ID,
			FailureKind:   domain.FailureQualityGate,
			ErrorSummary:  decisionError,
			HumanGuidance: humanGuidance,
		}, retryCfg, policy)
		if decision.Action != domain.RecoveryActionRerunPreviousAgent {
			haltLocalRepairLoop(exec, step)
			return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: attempt.ErrorSummary}, nil
		}
		return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: attempt.ErrorSummary}, nil
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// signalMergeReady implements the "signal_merge_ready" deterministic action.
// In a sub-execution (exec.StreamID set), marks the current stream as merge_ready.
// In a top-level execution, upgrades all completed streams to merge_ready.
func (h *Handlers) signalMergeReady(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	// In a sub-execution, only signal the current stream — it's still "executing"
	// at this point (advanceSubExecution marks it completed after we return).
	if exec.StreamID != "" {
		return h.signalStreamMergeReady(ctx, exec.StreamID, plan.ID, exec.ObjectiveID)
	}

	// Top-level execution: scan all completed streams.
	streamList, err := h.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing streams for plan: %s", err),
		}, nil
	}

	for _, stream := range streamList {
		if stream.Status != domain.StreamStatusCompleted {
			continue
		}
		if _, err := h.signalStreamMergeReady(ctx, stream.ID, plan.ID, exec.ObjectiveID); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("signaling stream %s merge_ready: %s", stream.ID, err),
			}, nil
		}
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// signalStreamMergeReady marks a single stream as merge_ready and enqueues it
// for immediate merge so dependent streams can start from merged upstream state.
func (h *Handlers) signalStreamMergeReady(ctx context.Context, streamID, planID, objectiveID string) (blueprint.StepResult, error) {
	if err := h.streams.UpdateStatus(ctx, streamID, domain.StreamStatusMergeReady); err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("updating stream %s to merge_ready: %s", streamID, err),
		}, nil
	}

	if h.mergeProcessor != nil {
		if err := h.mergeProcessor.EnqueueStream(ctx, streamID); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("enqueuing stream %s for merge: %s", streamID, err),
			}, nil
		}
	} else {
		stream, err := h.streams.Get(ctx, streamID)
		if err != nil {
			return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: fmt.Sprintf("loading stream %s for observability: %s", streamID, err)}, nil
		}
		if h.obs != nil {
			h.obs.RecordMilestone(observability.Milestone{
				EventType:   domain.EventMergeQueued,
				ProjectID:   stream.ProjectID,
				ObjectiveID: objectiveID,
				StreamID:    streamID,
				Status:      "queued",
				Details: map[string]any{
					"stream_id":    streamID,
					"plan_id":      planID,
					"objective_id": objectiveID,
				},
			})
		}
	}

	h.logger.Info("stream signaled for merge",
		"stream_id", streamID,
	)

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

func (h *Handlers) markStreamMerged(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	if exec.StreamID == "" {
		return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: "mark_stream_merged requires a stream sub-execution"}, nil
	}
	stream, err := h.streams.Get(ctx, exec.StreamID)
	if err != nil {
		return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: fmt.Sprintf("loading stream %s: %s", exec.StreamID, err)}, nil
	}
	stream.Status = domain.StreamStatusMerged
	if err := h.streams.Update(ctx, stream); err != nil {
		return blueprint.StepResult{Status: blueprint.StepStatusFailed, Error: fmt.Sprintf("marking stream %s merged: %s", exec.StreamID, err)}, nil
	}
	h.logger.Info("stream marked merged without merge queue", "stream_id", exec.StreamID, "execution_id", exec.ID)
	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// markComplete implements the "mark_complete" deterministic action.
// Checks stream statuses: if any streams failed, transitions to "partial" instead
// of "completed" so failed streams can be retried.
func (h *Handlers) markComplete(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	// Determine target status by checking stream outcomes.
	targetStatus := domain.ObjectiveStatusCompleted

	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err == nil {
		streams, err := h.streams.ListByPlan(ctx, plan.ID)
		if err == nil {
			for _, s := range streams {
				if s.Status == domain.StreamStatusFailed {
					targetStatus = domain.ObjectiveStatusPartial
					break
				}
			}
		}
	}

	if err := h.lifecycle.Transition(ctx, exec.ObjectiveID, targetStatus); err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("transitioning objective to %s: %s", targetStatus, err),
		}, nil
	}

	h.logger.Info("objective completed",
		"execution_id", exec.ID,
		"objective_id", exec.ObjectiveID,
		"status", targetStatus,
	)

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// createPR implements the "create_pr" deterministic action.
// Pushes the agent's branch to origin and creates a GitHub PR via `gh pr create`.
func (h *Handlers) createPR(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	obj, err := h.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	plan, planErr := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	streamSummary := ""
	hasMergedHead := false
	if planErr == nil {
		streamList, listErr := h.streams.ListByPlan(ctx, plan.ID)
		if listErr == nil && len(streamList) > 0 {
			var succeeded, failed []string
			for _, s := range streamList {
				switch s.Status {
				case domain.StreamStatusMerged:
					hasMergedHead = true
					succeeded = append(succeeded, s.Title)
				case domain.StreamStatusMergeReady, domain.StreamStatusCompleted:
					succeeded = append(succeeded, s.Title)
				case domain.StreamStatusFailed:
					failed = append(failed, s.Title)
				}
			}
			var sb strings.Builder
			if len(succeeded) > 0 {
				sb.WriteString("\n\n### Streams (succeeded)\n")
				for _, t := range succeeded {
					sb.WriteString(fmt.Sprintf("- %s\n", t))
				}
			}
			if len(failed) > 0 {
				sb.WriteString("\n### Streams (failed)\n")
				for _, t := range failed {
					sb.WriteString(fmt.Sprintf("- %s\n", t))
				}
			}
			streamSummary = sb.String()
		}
	}
	if !hasMergedHead {
		h.logger.Info("skipping PR creation because no merged head is available",
			"execution_id", exec.ID,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{
			Status: blueprint.StepStatusCompleted,
			Output: "skipped: no merged head available",
		}, nil
	}

	sb, err := h.findMergerSandbox(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  err.Error(),
		}, nil
	}

	// Check if origin remote exists — skip PR creation if not configured.
	remoteCheck, err := sb.Exec(ctx, "git remote get-url origin", sandbox.ExecOpts{})
	if err != nil || remoteCheck.ExitCode != 0 {
		h.logger.Info("no origin remote configured, skipping PR creation",
			"execution_id", exec.ID,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{
			Status: blueprint.StepStatusCompleted,
			Output: "skipped: no origin remote configured",
		}, nil
	}
	remoteURL := strings.TrimSpace(remoteCheck.Stdout)

	// Get the current branch name.
	branchResult, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil || branchResult.ExitCode != 0 {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting branch name: %s", branchResult.Stderr),
		}, nil
	}
	branch := strings.TrimSpace(branchResult.Stdout)

	// The branch should already be pushed by stream execution and by the merge
	// processor for multi-stream objectives. Avoid pushing again here because
	// create_pr runs immediately after merge completion and a redundant push can
	// race with the merge processor's final branch push.

	messages := generatedMessagesFromSource(exec, step.MessageSource)
	if drafted, draftErr := h.generatePRMessages(ctx, sb, step, obj, streamSummary, messages); draftErr != nil {
		h.logger.Warn("failed to generate PR metadata with agent", "objective_id", exec.ObjectiveID, "error", draftErr)
	} else {
		if drafted.PRTitle != "" {
			messages.PRTitle = drafted.PRTitle
		}
		if drafted.PRBody != "" {
			messages.PRBody = drafted.PRBody
		}
	}
	title := obj.Description
	if messages.PRTitle != "" {
		title = messages.PRTitle
	}
	body := fmt.Sprintf("Automated PR created by Tack.\n\nObjective: %s\nObjective ID: %s", obj.Description, obj.ID)
	if messages.PRBody != "" {
		body = messages.PRBody
	}
	body += streamSummary

	if prURL, apiErr := h.createPullRequestViaAPI(ctx, remoteURL, branch, title, body); apiErr == nil {
		h.logger.Info("PR created",
			"url", prURL,
			"branch", branch,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted, Output: prURL}, nil
	} else if apiErr != errPRAPIUnsupported {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("creating PR via api: %s", apiErr),
		}, nil
	}

	// Fallback to gh CLI inside the sandbox for unsupported remotes.
	escapedTitle := naming.ShellQuote(title)
	escapedBody := naming.ShellQuote(body)
	prCmd := fmt.Sprintf("gh pr create --title %s --body %s --head %s --base %s", escapedTitle, escapedBody, naming.ShellQuote(branch), naming.ShellQuote(h.baseBranch))

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

	prURL := strings.TrimSpace(prResult.Stdout)
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
// Prefers a lead agent sandbox; falls back to any agent sandbox (e.g. a single-blueprint builder-only flow).
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

// findMergerSandbox locates the merger sandbox for creating a PR.
// After multi-stream blueprints, the merger sandbox contains the final merged code.
// The merge processor tracks which stream sandbox was repurposed as the merger.
func (h *Handlers) findMergerSandbox(ctx context.Context, objectiveID string) (sandbox.Sandbox, error) {
	// Primary: ask the merge processor for the tracked merger sandbox.
	if mergerID := h.mergeProcessor.MergerSandboxID(objectiveID); mergerID != "" {
		sb, err := h.sandboxProvider.Get(ctx, mergerID)
		if err == nil {
			return sb, nil
		}
	}

	// Secondary: check agent sessions for a merger role (future agent-based mergers).
	sessions, err := h.agents.ListByObjective(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("listing agents for objective: %s", err)
	}

	for _, session := range sessions {
		if session.SandboxID == "" || session.Role != domain.AgentRoleMerger {
			continue
		}
		found, err := h.sandboxProvider.Get(ctx, session.SandboxID)
		if err != nil {
			continue
		}
		return found, nil
	}

	// No merger sandbox found — fall back for single-blueprint single-stream executions.
	return h.findSandboxForObjective(ctx, objectiveID)
}

var errPRAPIUnsupported = fmt.Errorf("pull request api unsupported")

type githubPullRequestResponse struct {
	HTMLURL string `json:"html_url"`
	Message string `json:"message"`
}

func (h *Handlers) createPullRequestViaAPI(ctx context.Context, remoteURL, head, title, body string) (string, error) {
	owner, repo, host, err := parseGitRemote(remoteURL)
	if err != nil {
		return "", errPRAPIUnsupported
	}
	if host != "github.com" || h.creds == nil {
		return "", errPRAPIUnsupported
	}
	tok, err := h.creds.GitToken(host)
	if err != nil {
		return "", errPRAPIUnsupported
	}

	payload := map[string]string{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  h.baseBranch,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	apiURL := strings.TrimRight(h.githubAPIBase, "/") + "/repos/" + owner + "/" + repo + "/pulls"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github api request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var parsed githubPullRequestResponse
	_ = json.Unmarshal(respBody, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(respBody))
		}
		return "", fmt.Errorf("github api status %d: %s", resp.StatusCode, msg)
	}
	if strings.TrimSpace(parsed.HTMLURL) == "" {
		return "", fmt.Errorf("github api returned empty pr url")
	}
	return strings.TrimSpace(parsed.HTMLURL), nil
}

func parseGitRemote(remote string) (owner, repo, host string, err error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("unsupported git remote: %s", remote)
		}
		host = parts[0]
		path := strings.TrimSuffix(strings.TrimPrefix(parts[1], "/"), ".git")
		segments := strings.Split(path, "/")
		if len(segments) < 2 {
			return "", "", "", fmt.Errorf("unsupported git remote path: %s", remote)
		}
		return segments[0], segments[1], host, nil
	}

	u, parseErr := url.Parse(remote)
	if parseErr != nil {
		return "", "", "", parseErr
	}
	host = u.Hostname()
	path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return "", "", "", fmt.Errorf("unsupported git remote path: %s", remote)
	}
	return segments[0], segments[1], host, nil
}

func (h *Handlers) generatePRMessages(ctx context.Context, sb sandbox.Sandbox, step *blueprint.Step, obj *domain.Objective, streamSummary string, seed agents.GeneratedMessages) (agents.GeneratedMessages, error) {
	if h.agentRuntime == nil {
		return agents.GeneratedMessages{}, nil
	}
	envVars := map[string]string{}
	if err := injectRuntimeCredentials(ctx, h.agentRuntime.Name(), h.runtimeAuth.Provider, h.creds, h.logger, sb, envVars); err != nil {
		return agents.GeneratedMessages{}, fmt.Errorf("preparing pr-writer auth: %w", err)
	}
	proc, err := h.agentRuntime.Spawn(ctx, sb, runtime.AgentOpts{
		Role:    "pr-writer",
		Model:   h.modelForDeterministicStep(step),
		Overlay: h.buildPRPrompt(ctx, sb, obj, streamSummary, seed),
		EnvVars: envVars,
	})
	if err != nil {
		return agents.GeneratedMessages{}, fmt.Errorf("spawning pr-writer agent: %w", err)
	}
	result, err := proc.Wait()
	if err != nil {
		return agents.GeneratedMessages{}, fmt.Errorf("waiting for pr-writer agent: %w", err)
	}
	if !result.Success {
		return agents.GeneratedMessages{}, fmt.Errorf("pr-writer agent failed: %s", strings.TrimSpace(result.Error))
	}
	_, msgs, found, err := agents.ExtractGeneratedMessages(result.Summary)
	if err != nil {
		return agents.GeneratedMessages{}, fmt.Errorf("parsing pr-writer messages: %w", err)
	}
	if !found {
		return agents.GeneratedMessages{}, fmt.Errorf("pr-writer agent returned no TACK_MESSAGES payload")
	}
	return msgs, nil
}

func (h *Handlers) modelForDeterministicStep(step *blueprint.Step) string {
	if step != nil && step.Model != "" {
		return step.Model
	}
	return h.defaultModel
}

func (h *Handlers) buildPRPrompt(ctx context.Context, sb sandbox.Sandbox, obj *domain.Objective, streamSummary string, seed agents.GeneratedMessages) string {
	var b strings.Builder
	b.WriteString("# Tack Agent: pr-writer\n\n")
	b.WriteString("You are drafting pull request metadata for already-merged changes.\n")
	b.WriteString("Do not modify files, do not run git commands, and do not create commits.\n")
	b.WriteString("Return exactly one final line starting with TACK_MESSAGES: containing compact JSON with pr_title and pr_body.\n\n")
	b.WriteString("## Objective\n")
	b.WriteString(obj.Description)
	b.WriteString("\n\n")
	b.WriteString("## Objective ID\n")
	b.WriteString(obj.ID)
	b.WriteString("\n\n")
	if seed.PRTitle != "" || seed.PRBody != "" {
		b.WriteString("## Existing Draft\n")
		if seed.PRTitle != "" {
			b.WriteString("Title: ")
			b.WriteString(seed.PRTitle)
			b.WriteString("\n")
		}
		if seed.PRBody != "" {
			b.WriteString("Body:\n")
			b.WriteString(seed.PRBody)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if streamSummary != "" {
		b.WriteString("## Stream Summary\n")
		b.WriteString(strings.TrimSpace(streamSummary))
		b.WriteString("\n\n")
	}
	diffRange := naming.ShellQuote("origin/" + h.baseBranch + "...HEAD")
	b.WriteString("## Changed Files\n")
	b.WriteString(h.commandOutput(ctx, sb, "git diff --name-only "+diffRange, 4000, "(changed files unavailable)"))
	b.WriteString("\n\n")
	b.WriteString("## Diff Stat\n")
	b.WriteString(h.commandOutput(ctx, sb, "git diff --stat "+diffRange, 6000, "(diff stat unavailable)"))
	b.WriteString("\n\n")
	b.WriteString("## Diff Excerpt\n")
	b.WriteString(h.commandOutput(ctx, sb, "git diff --unified=1 "+diffRange, 16000, "(diff excerpt unavailable)"))
	b.WriteString("\n\n")
	b.WriteString("## Output Contract\n")
	b.WriteString("Emit exactly one final line in this form:\n")
	b.WriteString("TACK_MESSAGES:{\"pr_title\":\"Concise title\",\"pr_body\":\"## Summary\\n- ...\\n\\n## Testing\\n- ...\"}\n")
	return b.String()
}

func (h *Handlers) commandOutput(ctx context.Context, sb sandbox.Sandbox, cmd string, maxLen int, fallback string) string {
	result, err := sb.Exec(ctx, cmd, sandbox.ExecOpts{})
	if err != nil || result.ExitCode != 0 {
		return fallback
	}
	out := strings.TrimSpace(result.Stdout)
	if out == "" {
		return fallback
	}
	if len(out) > maxLen {
		return out[:maxLen] + "\n...[truncated]"
	}
	return out
}

func generatedMessagesFromSource(exec *blueprint.Execution, sourceStepID string) agents.GeneratedMessages {
	if sourceStepID == "" || exec == nil || exec.StepStates == nil {
		return agents.GeneratedMessages{}
	}
	state := exec.StepStates[sourceStepID]
	if state == nil {
		return agents.GeneratedMessages{}
	}
	return agents.GeneratedMessagesFromMetadata(state.Metadata)
}

// formatGateErrors builds a structured error string from gate failures,
// including the command, exit code, and truncated stderr/stdout.
func formatGateErrors(result *gates.RunResult) string {
	const maxOutputLen = 2000
	truncate := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) <= maxOutputLen {
			return s
		}
		return "..." + s[len(s)-maxOutputLen:]
	}

	var b strings.Builder
	for _, gr := range result.Results {
		if gr.Passed {
			continue
		}
		fmt.Fprintf(&b, "gate %q failed (exit %d)\n", gr.Gate.Command, gr.ExitCode)
		if stderr := truncate(gr.Stderr); stderr != "" {
			fmt.Fprintf(&b, "stderr:\n%s\n", stderr)
		}
		if stdout := truncate(gr.Stdout); stdout != "" {
			fmt.Fprintf(&b, "stdout:\n%s\n", stdout)
		}
	}
	if b.Len() == 0 {
		return "one or more quality gates failed"
	}
	return strings.TrimSpace(b.String())
}

// gateErrorsInScope checks whether any gate failure references files within the stream's scope.
func gateErrorsInScope(result *gates.RunResult, fileScope []string) bool {
	for _, gr := range result.Results {
		if gr.Passed {
			continue
		}
		combined := gr.Stderr + "\n" + gr.Stdout
		errorFiles := gates.ExtractErrorFiles(combined)
		inScope := gates.FilesInScope(errorFiles, fileScope)
		if len(inScope) > 0 {
			return true
		}
	}
	return false
}

// formatGateErrorsScoped is like formatGateErrors but filters output to only
// include lines referencing files within the stream's file scope.
func formatGateErrorsScoped(result *gates.RunResult, fileScope []string) string {
	const maxOutputLen = 2000
	truncate := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) <= maxOutputLen {
			return s
		}
		return "..." + s[len(s)-maxOutputLen:]
	}

	var b strings.Builder
	for _, gr := range result.Results {
		if gr.Passed {
			continue
		}
		fmt.Fprintf(&b, "gate %q failed (exit %d)\n", gr.Gate.Command, gr.ExitCode)
		if stderr := truncate(gates.FilterOutputByScope(gr.Stderr, fileScope)); stderr != "" {
			fmt.Fprintf(&b, "stderr:\n%s\n", stderr)
		}
		if stdout := truncate(gates.FilterOutputByScope(gr.Stdout, fileScope)); stdout != "" {
			fmt.Fprintf(&b, "stdout:\n%s\n", stdout)
		}
	}
	if b.Len() == 0 {
		return "one or more quality gates failed (in-scope errors)"
	}
	return strings.TrimSpace(b.String())
}

// resetMergingEntry resets a stuck "merging" merge entry back to "pending"
// so the processor will pick it up again after a daemon restart.
func (h *Handlers) resetMergingEntry(ctx context.Context, streamID string) {
	if h.mergeProcessor != nil {
		h.mergeProcessor.ResetMergingEntries(ctx, streamID)
	}
}

// mergeQueue implements the "merge_queue" deterministic action.
// Partitions streams into merge_ready, failed, and active (executing/pending).
// Waits for active streams to resolve, then merges whatever is merge_ready.
// If all streams failed (zero merge_ready), returns completed — mark_complete
// handles setting the objective to partial status.
func (h *Handlers) mergeQueue(ctx context.Context, exec *blueprint.Execution) (blueprint.StepResult, error) {
	if h.mergeProcessor == nil {
		h.logger.Warn("merge processor not configured, skipping merge queue",
			"execution_id", exec.ID,
			"objective_id", exec.ObjectiveID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	plan, err := h.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan: %s", err),
		}, nil
	}

	allStreams, err := h.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing streams: %s", err),
		}, nil
	}

	// Partition streams by status.
	var toMerge []string
	var failed []string
	var active []string // executing or pending — still in progress
	for _, s := range allStreams {
		switch s.Status {
		case domain.StreamStatusMergeReady:
			toMerge = append(toMerge, s.ID)
		case domain.StreamStatusMerging:
			// Treat live merges as active work. Reclaiming them here races with the
			// merge processor and can roll a healthy in-flight merge back to
			// merge_ready/pending.
			active = append(active, s.ID)
		case domain.StreamStatusFailed:
			failed = append(failed, s.ID)
		case domain.StreamStatusExecuting, domain.StreamStatusPending:
			active = append(active, s.ID)
		case domain.StreamStatusMerged, domain.StreamStatusCompleted:
			// Already merged or completed — nothing to do.
		}
	}

	h.logger.Info("merge queue partitioned streams",
		"execution_id", exec.ID,
		"merge_ready", len(toMerge),
		"failed", len(failed),
		"active", len(active),
	)

	// If streams are still active, wait for them to resolve before proceeding.
	if len(active) > 0 {
		sub, unsub := h.eventBus.Subscribe(64)
		defer unsub()

		activeSet := make(map[string]bool, len(active))
		for _, id := range active {
			activeSet[id] = true
		}

		for len(activeSet) > 0 {
			select {
			case <-ctx.Done():
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  "context cancelled while waiting for active streams",
				}, nil
			case event, ok := <-sub:
				if !ok {
					return blueprint.StepResult{
						Status: blueprint.StepStatusFailed,
						Error:  "event subscription closed while waiting for active streams",
					}, nil
				}
				if !activeSet[event.Stream] {
					continue
				}
				switch event.Type {
				case domain.EventMergeQueued:
					// Stream became merge_ready — move to toMerge.
					delete(activeSet, event.Stream)
					toMerge = append(toMerge, event.Stream)
				case domain.EventAgentFailed, domain.EventMergeFailed:
					// Stream failed — move to failed.
					delete(activeSet, event.Stream)
					failed = append(failed, event.Stream)
				case domain.EventAgentCompleted, domain.EventMergePublished:
					// Re-check status to determine whether the stream still needs a top-level
					// merge enqueue or is already fully merged.
					stream, err := h.streams.Get(ctx, event.Stream)
					if err != nil {
						delete(activeSet, event.Stream)
						failed = append(failed, event.Stream)
						continue
					}
					switch stream.Status {
					case domain.StreamStatusMergeReady:
						delete(activeSet, event.Stream)
						toMerge = append(toMerge, event.Stream)
					case domain.StreamStatusFailed:
						delete(activeSet, event.Stream)
						failed = append(failed, event.Stream)
					case domain.StreamStatusMerged, domain.StreamStatusCompleted:
						delete(activeSet, event.Stream)
					}
					// Otherwise, still active — keep waiting.
				}
			}
		}

		h.logger.Info("all active streams resolved",
			"execution_id", exec.ID,
			"merge_ready", len(toMerge),
			"failed", len(failed),
		)
	}

	// If no streams are merge_ready (all failed), return completed.
	// mark_complete will set the objective to partial status.
	if len(toMerge) == 0 {
		h.logger.Info("no merge_ready streams, skipping merge",
			"execution_id", exec.ID,
			"failed_count", len(failed),
		)
		return blueprint.StepResult{
			Status: blueprint.StepStatusCompleted,
			Output: fmt.Sprintf("no streams to merge (%d failed)", len(failed)),
		}, nil
	}

	// Subscribe to merge events BEFORE enqueuing so we don't miss any.
	mergeSub, mergeUnsub := h.eventBus.Subscribe(64)
	defer mergeUnsub()

	// Enqueue — fail the step if any enqueue errors.
	for _, streamID := range toMerge {
		if err := h.mergeProcessor.EnqueueStream(ctx, streamID); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("enqueuing stream %s: %s", streamID, err),
			}, nil
		}
	}

	// Block until all enqueued merges resolve.
	pending := make(map[string]bool, len(toMerge))
	for _, id := range toMerge {
		pending[id] = true
	}

	var mergeFailures []string
	for len(pending) > 0 {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  "context cancelled while waiting for merges",
			}, nil
		case event, ok := <-mergeSub:
			if !ok {
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  "event subscription closed while waiting for merges",
				}, nil
			}
			if !pending[event.Stream] {
				continue
			}
			switch event.Type {
			case domain.EventMergePublished:
				delete(pending, event.Stream)
			case domain.EventMergeFailed:
				delete(pending, event.Stream)
				var payload map[string]string
				_ = json.Unmarshal([]byte(event.Payload), &payload)
				mergeFailures = append(mergeFailures, fmt.Sprintf("stream %s: %s", event.Stream, payload["error"]))
			}
		}
	}

	if len(mergeFailures) > 0 {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("merge failures: %s", strings.Join(mergeFailures, "; ")),
		}, nil
	}

	if len(failed) > 0 {
		return blueprint.StepResult{
			Status: blueprint.StepStatusCompleted,
			Output: fmt.Sprintf("partial: %d streams merged, %d streams failed", len(toMerge), len(failed)),
		}, nil
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}
