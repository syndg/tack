package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/sandbox"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// escalateStreamFailure publishes an EventEscalation with structured context
// about a stream failure so that @human can review and potentially retry.
func (c *Coordinator) escalateStreamFailure(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step, streamID, errMsg string) {
	stream, err := c.streams.Get(ctx, streamID)
	if err != nil {
		c.logger.Error("failed to load stream for escalation",
			"stream_id", streamID,
			"error", err,
		)
		return
	}

	// Determine context level from escalation config.
	contextLevel := "full"
	prompt := "How should the agent resolve this?"
	if step.Escalation != nil {
		contextLevel = step.Escalation.EffectiveContext()
		prompt = step.Escalation.EffectivePrompt()
	}

	payload := map[string]string{
		"stream_id":     streamID,
		"stream_title":  stream.Title,
		"execution_id":  exec.ID,
		"error":         errMsg,
		"context_level": contextLevel,
		"prompt":        prompt,
	}
	if stream.ExecutionID != "" {
		payload["sub_execution_id"] = stream.ExecutionID
	}
	payloadJSON, _ := json.Marshal(payload)

	// Route through mail broker so the escalation is persisted and visible
	// via `deck mail` / unread-mail APIs, not just the event stream.
	if c.mailSender != nil {
		msg := &domain.MailMessage{
			From:      "coordinator",
			To:        "@human",
			Subject:   "Stream failed: " + stream.Title,
			Body:      errMsg,
			Type:      "escalation",
			Priority:  "high",
			Payload:   string(payloadJSON),
			Objective: exec.ObjectiveID,
			Stream:    streamID,
		}
		if err := c.mailSender.Send(ctx, msg); err != nil {
			c.logger.Error("failed to send stream failure escalation via broker", "error", err)
			// Fall back to direct event publish
			c.eventBus.Publish(domain.Event{
				Type:      domain.EventEscalation,
				Objective: exec.ObjectiveID,
				Stream:    streamID,
				Payload:   string(payloadJSON),
				CreatedAt: time.Now(),
			})
		}
	} else {
		c.eventBus.Publish(domain.Event{
			Type:      domain.EventEscalation,
			Objective: exec.ObjectiveID,
			Stream:    streamID,
			Payload:   string(payloadJSON),
			CreatedAt: time.Now(),
		})
	}

	c.logger.Info("stream failure escalated to human",
		"stream_id", streamID,
		"stream_title", stream.Title,
		"context_level", contextLevel,
		"execution_id", exec.ID,
	)
}

// Retry retries a failed stream's sub-execution with optional human guidance.
// Creates a fresh sub-execution of the same blueprint and injects the previous
// error + guidance as fix_context on the first agent step.
func (c *Coordinator) Retry(ctx context.Context, failedExecID string, guidance string) error {
	failedExec, err := c.executions.Get(ctx, failedExecID)
	if err != nil {
		return fmt.Errorf("getting execution %s: %w", failedExecID, ErrNotFound)
	}
	if failedExec.Status != "failed" {
		return fmt.Errorf("execution %s is not failed (status: %s): %w", failedExecID, failedExec.Status, ErrInvalidState)
	}
	if failedExec.StreamID == "" {
		return fmt.Errorf("execution %s is not a stream sub-execution: %w", failedExecID, ErrInvalidState)
	}

	// 2. Get the stream and verify it's failed.
	stream, err := c.streams.Get(ctx, failedExec.StreamID)
	if err != nil {
		return fmt.Errorf("getting stream %s: %w", failedExec.StreamID, err)
	}
	if stream.Status != "failed" {
		return fmt.Errorf("stream %s is not failed (status: %s)", stream.ID, stream.Status)
	}

	// 3. Resolve the blueprint used by the failed sub-execution.
	refBP, ok := c.engine.GetBlueprint(failedExec.BlueprintName)
	if !ok {
		return fmt.Errorf("blueprint %q not found", failedExec.BlueprintName)
	}

	// 4. Collect the last error from the failed execution for fix context.
	var lastError string
	for _, state := range failedExec.StepStates {
		if state.Status == blueprint.StepStatusFailed && state.Error != "" {
			lastError = state.Error
			break
		}
	}

	// 5. Build combined fix context from error + human guidance.
	var fixContext strings.Builder
	if lastError != "" {
		fmt.Fprintf(&fixContext, "Previous error:\n%s\n", lastError)
	}
	if guidance != "" {
		fmt.Fprintf(&fixContext, "\nHuman guidance:\n%s\n", guidance)
	}

	// 6. Reset stream to pending.
	if err := c.streams.UpdateStatus(ctx, stream.ID, "pending"); err != nil {
		return fmt.Errorf("resetting stream %s to pending: %w", stream.ID, err)
	}
	if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
		return fmt.Errorf("marking stream %s executing: %w", stream.ID, err)
	}

	// 7. Create a new sub-execution.
	subExec, err := c.engine.Start(ctx, refBP.Name, failedExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("starting retry sub-execution: %w", err)
	}
	subExec.ParentID = failedExec.ParentID
	subExec.StreamID = stream.ID

	// 8. Inject fix context on the first agent step.
	if fixCtx := fixContext.String(); fixCtx != "" {
		for _, step := range refBP.Steps {
			if step.Type == blueprint.StepTypeAgent {
				if state := subExec.StepStates[step.ID]; state != nil {
					state.Metadata = map[string]string{
						"fix_context": fixCtx,
					}
				}
				break
			}
		}
	}

	if err := c.executions.Create(ctx, subExec); err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("persisting retry sub-execution: %w", err)
	}

	// Link stream to new sub-execution.
	if err := c.streams.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		c.logger.Warn("failed to link stream to retry sub-execution",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
			"error", err,
		)
	}

	c.logger.Info("retrying stream sub-execution",
		"stream_id", stream.ID,
		"failed_execution_id", failedExecID,
		"new_execution_id", subExec.ID,
		"has_guidance", guidance != "",
	)

	// 9. Get the plan ID for MarkCompleted cascade.
	plan, err := c.plans.GetByObjective(ctx, failedExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("getting plan for objective: %w", err)
	}

	// 10. Launch the sub-execution in a goroutine and handle completion.
	// Register the cancel func in activeExecs so Stop() can cancel retry goroutines.
	baseCtx := c.ctx
	if baseCtx == nil {
		baseCtx = ctx
	}
	execCtx, cancel := context.WithCancel(baseCtx)

	retryKey := "retry:" + subExec.ID
	c.mu.Lock()
	c.activeExecs[retryKey] = cancel
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.activeExecs, retryKey)
			c.mu.Unlock()
			cancel()
		}()

		results := make(chan streamResult, 1)
		c.advanceSubExecution(execCtx, subExec, stream, plan.ID, results)
		res := <-results

		if res.Error != "" {
			c.logger.Warn("retry sub-execution failed",
				"stream_id", stream.ID,
				"execution_id", subExec.ID,
				"error", res.Error,
			)
			return
		}

		c.logger.Info("retry sub-execution completed",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
		)

		// Enqueue the retried stream for merge (the parent merge step already ran).
		if c.mergeEnqueuer != nil {
			if err := c.mergeEnqueuer.EnqueueStream(execCtx, stream.ID); err != nil {
				c.logger.Warn("failed to enqueue retried stream for merge",
					"stream_id", stream.ID,
					"error", err,
				)
			}
		}

		// Check if all streams are now completed — upgrade objective from partial to completed.
		c.checkPartialToCompleted(execCtx, failedExec.ObjectiveID)
	}()

	return nil
}

// checkPartialToCompleted checks if an objective in "partial" status can be
// upgraded to "completed" (all streams now completed).
func (c *Coordinator) checkPartialToCompleted(ctx context.Context, objectiveID string) {
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil || obj.Status != domain.ObjectiveStatusPartial {
		return
	}

	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return
	}
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return
	}

	for _, s := range streams {
		if s.Status != "completed" && s.Status != domain.StreamStatusMerged {
			return // still have non-terminal streams (merge_ready/merging not final)
		}
	}

	// All streams are fully merged or completed.
	// Re-push the merger branch first — if push fails, stay partial so the
	// user can retry. Only transition to completed if push succeeds (or if
	// no remote is configured, in which case push is skipped).
	if !lifecycle.IsValidTransition(obj.Status, domain.ObjectiveStatusCompleted) {
		return
	}

	if !c.rePushMergerBranch(ctx, objectiveID) {
		c.logger.Warn("staying partial — merger branch re-push failed, retry possible",
			"objective_id", objectiveID,
		)
		return
	}

	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusCompleted); err != nil {
		c.logger.Warn("failed to upgrade objective from partial to completed",
			"objective_id", objectiveID,
			"error", err,
		)
	} else {
		c.logger.Info("objective upgraded from partial to completed",
			"objective_id", objectiveID,
		)
		c.cleanupObjectiveSandboxes(ctx, objectiveID)
	}
}

// rePushMergerBranch pushes the merger branch to origin after a retry completes.
// Returns true if push succeeded or was skipped (no remote), false on push failure.
func (c *Coordinator) rePushMergerBranch(ctx context.Context, objectiveID string) bool {
	mergerID := c.mergeEnqueuer.MergerSandboxID(objectiveID)
	if mergerID == "" {
		// No merger sandbox — likely a simple/hotfix objective with no merge step.
		return true
	}
	sb, err := c.spawner.GetSandbox(ctx, mergerID)
	if err != nil {
		c.logger.Warn("failed to get merger sandbox", "sandbox_id", mergerID, "error", err)
		return false
	}

	// Check if origin remote exists — if not, push is not applicable.
	remoteCheck, err := sb.Exec(ctx, "git remote get-url origin", sandbox.ExecOpts{})
	if err != nil || remoteCheck.ExitCode != 0 {
		return true // no remote configured, nothing to push
	}

	branchResult, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil || branchResult.ExitCode != 0 {
		c.logger.Warn("failed to get merger branch name", "objective", objectiveID)
		return false
	}
	branch := strings.TrimSpace(branchResult.Stdout)

	pushResult, err := sb.Exec(ctx, fmt.Sprintf("git push -f origin %s", branch), sandbox.ExecOpts{})
	if err != nil || pushResult.ExitCode != 0 {
		c.logger.Warn("failed to re-push merger branch after retry",
			"objective", objectiveID,
			"branch", branch,
			"error", pushResult.Stderr,
		)
		return false
	}

	c.logger.Info("re-pushed merger branch after retry",
		"objective", objectiveID,
		"branch", branch,
	)
	return true
}

func (c *Coordinator) completeExecution(ctx context.Context, objectiveID string) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load plan on execution completion", "objective_id", objectiveID, "error", err)
		return
	}

	// Check stream statuses to determine if the outcome is partial.
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		c.logger.Error("failed to list streams on execution completion", "objective_id", objectiveID, "error", err)
	}

	hasFailedStreams := false
	for _, s := range streams {
		if s.Status == "failed" {
			hasFailedStreams = true
			break
		}
	}

	// For single-stream executions, mark the stream completed if still active.
	if len(streams) == 1 {
		s := streams[0]
		if s.Status == "pending" || s.Status == "executing" {
			if err := c.scheduler.MarkCompleted(ctx, s.ID, plan.ID); err != nil {
				c.logger.Error("failed to mark single stream completed", "stream_id", s.ID, "error", err)
			}
		}
	}

	// Determine target status: partial if some streams failed, completed otherwise.
	targetStatus := domain.ObjectiveStatusCompleted
	planStatus := domain.PlanStatusCompleted
	if hasFailedStreams {
		targetStatus = domain.ObjectiveStatusPartial
		// Plan is still "completed" — the execution finished, some streams just failed.
	}

	if err := c.plans.UpdateStatus(ctx, plan.ID, planStatus); err != nil {
		c.logger.Error("failed to mark plan completed", "plan_id", plan.ID, "error", err)
	}

	// Safety net: ensure the objective reaches the right terminal state even if
	// mark_complete already handled it (Transition is a no-op if status is already terminal).
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load objective on execution completion", "objective_id", objectiveID, "error", err)
		return
	}
	if obj.Status != domain.ObjectiveStatusCompleted && obj.Status != domain.ObjectiveStatusFailed && obj.Status != domain.ObjectiveStatusPartial {
		if lifecycle.IsValidTransition(obj.Status, targetStatus) {
			if err := c.lifecycle.Transition(ctx, objectiveID, targetStatus); err != nil {
				c.logger.Warn("safety-net objective completion transition failed",
					"objective_id", objectiveID,
					"current_status", obj.Status,
					"target_status", targetStatus,
					"error", err,
				)
			}
		}
	}

	// Clean up sandboxes only when fully completed — partial objectives
	// retain their sandboxes so failed streams can be retried and merged.
	if targetStatus == domain.ObjectiveStatusCompleted {
		c.cleanupObjectiveSandboxes(ctx, objectiveID)
	}
}

func (c *Coordinator) failExecution(ctx context.Context, objectiveID, reason string) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err == nil {
		if err := c.plans.UpdateStatus(ctx, plan.ID, domain.PlanStatusFailed); err != nil {
			c.logger.Error("failed to mark plan failed", "plan_id", plan.ID, "error", err)
		}
	} else {
		c.logger.Error("failed to load plan on execution failure", "objective_id", objectiveID, "error", err)
	}

	stream, streamErr := c.singleStreamForObjective(ctx, objectiveID)
	if streamErr != nil {
		c.logger.Error("failed to load single stream on execution failure", "objective_id", objectiveID, "error", streamErr)
	} else if stream != nil && (stream.Status == "pending" || stream.Status == "executing") {
		if err := c.scheduler.MarkFailed(ctx, stream.ID); err != nil {
			c.logger.Error("failed to mark single stream failed", "stream_id", stream.ID, "error", err)
		}
	}

	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		c.logger.Error("failed to load objective on execution failure", "objective_id", objectiveID, "error", err)
		return
	}
	if obj.Status != domain.ObjectiveStatusFailed && lifecycle.IsValidTransition(obj.Status, domain.ObjectiveStatusFailed) {
		if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusFailed); err != nil {
			c.logger.Error("failed to transition objective to failed", "objective_id", objectiveID, "reason", reason, "error", err)
		}
	}

	// Clean up sandboxes and branches for this objective.
	c.cleanupObjectiveSandboxes(ctx, objectiveID)
}

// cleanupObjectiveSandboxes removes all sandboxes and branches for a completed objective.
// Runs in a background goroutine to avoid blocking the completion flow.
func (c *Coordinator) cleanupObjectiveSandboxes(ctx context.Context, objectiveID string) {
	go func() {
		c.spawner.CleanupObjective(ctx, objectiveID)
	}()
}
