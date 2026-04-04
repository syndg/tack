package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/services/agents"
)

// HandleAgentStep implements the StepHandler for agent-type blueprint steps.
// Registered with the engine as the StepTypeAgent handler.
// For single-stream executions (e.g. Hotfix) the lone stream is attached to the
// agent session so stream state and quality gates operate on the same sandbox.
func (c *Coordinator) HandleAgentStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	role := step.Role
	if role == "" {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q has no role defined", step.ID),
		}, nil
	}

	obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	// In a sub-execution, the stream is identified by exec.StreamID.
	// For top-level single-blueprint executions, fall back to
	// singleStreamForObjective.
	var stream *domain.Stream
	if exec.StreamID != "" {
		stream, err = c.streams.Get(ctx, exec.StreamID)
		if err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("getting stream %s: %s", exec.StreamID, err),
			}, nil
		}
	} else {
		stream, err = c.singleStreamForObjective(ctx, exec.ObjectiveID)
		if err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("getting stream context: %s", err),
			}, nil
		}
	}
	if stream != nil && stream.Status == domain.StreamStatusPending {
		if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("marking stream %s executing: %s", stream.ID, err),
			}, nil
		}
	}

	taskSpec := step.Description
	if taskSpec == "" && stream != nil {
		taskSpec = stream.Description
	}

	// Extract fix context from previous iteration (for fix-loop retries).
	var fixContext string
	if state := exec.StepStates[step.ID]; state != nil && state.Metadata != nil {
		fixContext = state.Metadata["fix_context"]
	}

	// Spawn the agent. The spawner handles sandbox reuse internally —
	// it looks up existing stream sandboxes via labels.
	result, err := c.spawner.Spawn(ctx, SpawnRequest{
		Objective:   obj,
		Stream:      stream,
		Role:        role,
		Model:       c.modelForAgentStep(step),
		TaskSpec:    taskSpec,
		ExecutionID: exec.ID,
		CommitMode:  string(step.EffectiveCommitMode()),
		Messages:    step.Messages,
		FixContext:  fixContext,
	})
	if err != nil {
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("spawning agent for step %q: %s", step.ID, err),
		}, nil
	}

	// Track the spawn result.
	c.tracker.Track(result.Session, result.Process)

	c.logger.Info("agent spawned for step",
		"session_id", result.Session.ID,
		"role", role,
		"step", step.ID,
		"execution_id", exec.ID,
	)

	// Block until agent completes.
	agentResult, waitErr := result.Process.Wait()

	wasKilled := c.tracker.Finish(result.Session.ID)
	if wasKilled {
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		c.spawner.MarkFailed(ctx, result.Session, "killed")
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q failed: killed", step.ID),
		}, nil
	}

	if waitErr != nil || !agentResult.Success {
		errMsg := agentResult.Error
		if waitErr != nil {
			errMsg = waitErr.Error()
		}
		if stream != nil {
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
		}
		c.spawner.MarkFailed(ctx, result.Session, errMsg)
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q failed: %s", step.ID, errMsg),
		}, nil
	}

	cleanSummary, generatedMessages := c.extractGeneratedMessages(step, agentResult.Summary)

	// Remove runtime artifacts before any status/commit/push operations so they
	// never leak into stream branches or merger branches.
	if err := c.cleanupRuntimeArtifacts(ctx, result.Sandbox); err != nil {
		c.logger.Warn("failed to clean runtime artifacts", "step", step.ID, "error", err)
	}

	// Handle commit mode after successful agent completion.
	commitMode := step.EffectiveCommitMode()
	switch commitMode {
	case blueprint.CommitModeAuto:
		if err := c.autoCommit(ctx, result.Sandbox, obj.Description, generatedMessages.CommitMessage); err != nil {
			c.logger.Error("auto-commit failed", "step", step.ID, "error", err)
			// Non-fatal: changes are still in the worktree for the merge processor.
		}
	case blueprint.CommitModeAgent:
		// Verify the agent actually committed; fall back to auto if not.
		statusResult, err := result.Sandbox.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
		if err == nil && strings.TrimSpace(statusResult.Stdout) != "" {
			c.logger.Warn("agent mode set but uncommitted changes found, falling back to auto-commit", "step", step.ID)
			if err := c.autoCommit(ctx, result.Sandbox, obj.Description, generatedMessages.CommitMessage); err != nil {
				c.logger.Error("fallback auto-commit failed", "step", step.ID, "error", err)
			}
		}
	case blueprint.CommitModeNone:
		// No commit needed.
	}

	// Push the branch to origin so the merger sandbox can fetch it later.
	// Required for remote sandboxes (Daytona) where each sandbox is a separate
	// clone. Must happen right after commit while the sandbox is still alive —
	// remote sandboxes may auto-stop before the merge step runs.
	if stream != nil && role != string(domain.AgentRolePlanner) {
		branchRes, _ := result.Sandbox.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
		if branchRes.ExitCode == 0 {
			branch := strings.TrimSpace(branchRes.Stdout)
			pushRes, pushErr := result.Sandbox.Exec(ctx, fmt.Sprintf("git push -u origin %s", branch), sandbox.ExecOpts{})
			if pushErr != nil || pushRes.ExitCode != 0 {
				c.logger.Warn("failed to push branch (merge may fail for remote sandboxes)",
					"branch", branch, "step", step.ID, "exit", pushRes.ExitCode)
			}
		}
	}

	// If this was a planner agent, create the plan from its output.
	if role == string(domain.AgentRolePlanner) && c.planCreator != nil {
		plan, err := c.planCreator.CreatePlan(ctx, exec.ObjectiveID, cleanSummary)
		if err != nil {
			c.spawner.MarkFailed(ctx, result.Session, fmt.Sprintf("plan creation failed: %s", err))
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("creating plan from planner output: %s", err),
			}, nil
		}
		c.logger.Info("plan created from planner agent",
			"plan_id", plan.ID,
			"objective_id", exec.ObjectiveID,
		)

		// Delete planner sandbox — it's no longer needed and frees resources
		// for the stream agents (important for Daytona tier CPU limits).
		if err := c.spawner.DeleteSandbox(ctx, result.Sandbox.ID()); err != nil {
			c.logger.Warn("failed to delete planner sandbox", "sandbox_id", result.Sandbox.ID(), "error", err)
		}
	}

	c.spawner.MarkCompleted(ctx, result.Session, cleanSummary)
	return blueprint.StepResult{
		Status:   blueprint.StepStatusCompleted,
		Output:   cleanSummary,
		Metadata: generatedMessages.ToMetadata(),
	}, nil
}

func (c *Coordinator) modelForAgentStep(step *blueprint.Step) string {
	if step != nil && step.Model != "" {
		return step.Model
	}
	if step != nil && step.Role == string(domain.AgentRolePlanner) {
		return c.plannerModel
	}
	return c.agentModel
}

// streamResult reports the outcome of a stream's sub-execution.
type streamResult struct {
	StreamID string
	Error    string // empty on success
}

// HandleBlueprintRefStep implements the StepHandler for blueprint_ref steps.
// Creates a sub-execution of the referenced blueprint per stream and advances
// them concurrently. Handles dependency cascade, failure escalation, and partial
// completion.
func (c *Coordinator) HandleBlueprintRefStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	if step.Foreach != "work_item" {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("blueprint_ref step %q must declare foreach: work_item", step.ID),
		}, nil
	}

	// 1. Resolve the referenced blueprint.
	refBP := c.resolveBlueprint(step.Ref)
	if refBP == nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("referenced blueprint %q not found", step.Ref),
		}, nil
	}

	// 2. Get the plan and all its streams.
	plan, err := c.plans.GetByObjective(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting plan for objective: %s", err),
		}, nil
	}

	allStreams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("listing streams for plan %s: %s", plan.ID, err),
		}, nil
	}

	if len(allStreams) == 0 {
		c.logger.Info("no streams for plan, completing blueprint_ref step immediately",
			"plan_id", plan.ID,
			"execution_id", exec.ID,
		)
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	totalStreams := len(allStreams)

	// Build stream ID set and pre-count already-resolved streams.
	// After a daemon restart, streams may sit in terminal states (merge_ready,
	// merged, failed) that will never emit a new result. Count them as resolved
	// so the collection loop doesn't block forever.
	streamSet := make(map[string]bool, totalStreams)
	resolvedCount := 0
	var failures []string
	for _, s := range allStreams {
		streamSet[s.ID] = true
		switch s.Status {
		case domain.StreamStatusCompleted, domain.StreamStatusMergeReady, domain.StreamStatusMerged:
			resolvedCount++
		case domain.StreamStatusFailed:
			resolvedCount++
			failures = append(failures, fmt.Sprintf("stream %s: previously failed", s.ID))
		}
	}
	if resolvedCount == totalStreams {
		if len(failures) > 0 {
			return blueprint.StepResult{
				Status:   blueprint.StepStatusCompleted,
				Output:   fmt.Sprintf("partial: %d/%d streams completed; failures: %s", totalStreams-len(failures), totalStreams, strings.Join(failures, "; ")),
				Metadata: map[string]string{"partial": "true", "failures": fmt.Sprintf("%d", len(failures))},
			}, nil
		}
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	// Determine failure policy.
	onWorkItemFailure := step.OnWorkItemFailure
	if onWorkItemFailure == "" {
		onWorkItemFailure = "escalate"
	}

	// 3. Subscribe to stream-ready events for cascade.
	sub, unsub := c.eventBus.Subscribe(128)
	defer unsub()

	// Results channel collects sub-execution outcomes.
	results := make(chan streamResult, totalStreams)

	// 4. Start sub-executions for initially ready streams.
	ready, err := c.scheduler.GetReadyStreams(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting ready streams: %s", err),
		}, nil
	}

	for i := range ready {
		stream := ready[i]
		if err := c.startStreamSubExecution(ctx, exec, refBP, &stream, plan.ID, results); err != nil {
			c.logger.Error("failed to start sub-execution for stream",
				"stream_id", stream.ID,
				"error", err,
			)
			results <- streamResult{StreamID: stream.ID, Error: fmt.Sprintf("failed to start: %v", err)}
		}
	}

	for resolvedCount < totalStreams {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  "execution context cancelled",
			}, nil

		case res := <-results:
			resolvedCount++
			if res.Error != "" {
				failures = append(failures, fmt.Sprintf("stream %s: %s", res.StreamID, res.Error))

				if onWorkItemFailure == "escalate" {
					c.escalateStreamFailure(ctx, exec, step, res.StreamID, res.Error)
				}

				c.logger.Warn("stream sub-execution failed",
					"stream_id", res.StreamID,
					"error", res.Error,
					"resolved", resolvedCount,
					"total", totalStreams,
				)
			} else {
				c.logger.Info("stream sub-execution completed",
					"stream_id", res.StreamID,
					"resolved", resolvedCount,
					"total", totalStreams,
				)
			}

		case event, ok := <-sub:
			if !ok {
				continue
			}
			if event.Type != domain.EventStreamReady {
				continue
			}
			var readyPayload map[string]string
			if err := json.Unmarshal([]byte(event.Payload), &readyPayload); err != nil {
				continue
			}
			streamID := readyPayload["stream_id"]
			if !streamSet[streamID] {
				continue
			}
			stream, err := c.streams.Get(ctx, streamID)
			if err != nil || stream.Status != domain.StreamStatusPending {
				continue
			}
			if err := c.startStreamSubExecution(ctx, exec, refBP, stream, plan.ID, results); err != nil {
				c.logger.Error("failed to start cascade sub-execution",
					"stream_id", streamID,
					"error", err,
				)
				results <- streamResult{StreamID: streamID, Error: fmt.Sprintf("failed to start: %v", err)}
			}
		}
	}

	// 6. Determine final result.
	if len(failures) > 0 {
		return blueprint.StepResult{
			Status:   blueprint.StepStatusCompleted,
			Output:   fmt.Sprintf("partial: %d/%d streams completed; failures: %s", totalStreams-len(failures), totalStreams, strings.Join(failures, "; ")),
			Metadata: map[string]string{"partial": "true", "failures": fmt.Sprintf("%d", len(failures))},
		}, nil
	}

	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// resolveBlueprint resolves a blueprint ref by blueprint ID.
func (c *Coordinator) resolveBlueprint(ref string) *blueprint.Blueprint {
	if bp, ok := c.engine.GetBlueprint(ref); ok {
		return bp
	}
	return nil
}

// startStreamSubExecution creates a sub-execution for a stream and launches
// advanceSubExecution in a goroutine.
func (c *Coordinator) startStreamSubExecution(
	ctx context.Context,
	parentExec *blueprint.Execution,
	refBP *blueprint.Blueprint,
	stream *domain.Stream,
	planID string,
	results chan<- streamResult,
) error {
	if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
		return fmt.Errorf("marking stream executing: %w", err)
	}

	// Create sub-execution.
	subExec, err := c.engine.Start(ctx, refBP.ID, parentExec.ObjectiveID)
	if err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("starting sub-execution: %w", err)
	}
	subExec.ParentID = parentExec.ID
	subExec.StreamID = stream.ID

	if err := c.executions.Create(ctx, subExec); err != nil {
		_ = c.scheduler.MarkFailed(ctx, stream.ID)
		return fmt.Errorf("persisting sub-execution: %w", err)
	}

	// Link stream to sub-execution.
	if err := c.streams.UpdateExecutionID(ctx, stream.ID, subExec.ID); err != nil {
		c.logger.Warn("failed to link stream to sub-execution",
			"stream_id", stream.ID,
			"execution_id", subExec.ID,
			"error", err,
		)
	}

	c.logger.Info("started stream sub-execution",
		"stream_id", stream.ID,
		"sub_execution_id", subExec.ID,
		"blueprint_id", refBP.ID,
		"parent_execution_id", parentExec.ID,
	)

	go c.advanceSubExecution(ctx, subExec, stream, planID, results)
	return nil
}

// advanceSubExecution drives a stream's sub-execution to completion in a goroutine.
// Reports the result via the results channel.
func (c *Coordinator) advanceSubExecution(
	ctx context.Context,
	subExec *blueprint.Execution,
	stream *domain.Stream,
	planID string,
	results chan<- streamResult,
) {
	for {
		if ctx.Err() != nil {
			results <- streamResult{StreamID: stream.ID, Error: "context cancelled"}
			return
		}

		updated, err := c.engine.Advance(ctx, subExec)
		if err != nil {
			if ctx.Err() != nil {
				results <- streamResult{StreamID: stream.ID, Error: "context cancelled"}
				return
			}
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
			results <- streamResult{StreamID: stream.ID, Error: fmt.Sprintf("advance failed: %v", err)}
			return
		}
		subExec = updated

		// Persist state after each advance.
		if err := c.executions.Update(ctx, subExec); err != nil {
			c.logger.Error("failed to persist sub-execution state",
				"execution_id", subExec.ID,
				"stream_id", stream.ID,
				"error", err,
			)
		}

		switch subExec.Status {
		case "completed":
			if err := c.scheduler.MarkCompleted(ctx, stream.ID, planID); err != nil {
				c.logger.Error("failed to mark stream completed",
					"stream_id", stream.ID,
					"error", err,
				)
			}
			results <- streamResult{StreamID: stream.ID}
			return
		case "failed":
			_ = c.scheduler.MarkFailed(ctx, stream.ID)
			failErr := "sub-execution failed"
			if subExec.CurrentStep != "" {
				if state := subExec.StepStates[subExec.CurrentStep]; state != nil && state.Error != "" {
					failErr = state.Error
				}
			}
			// Find the last failed step's error from step states.
			for _, state := range subExec.StepStates {
				if state.Status == blueprint.StepStatusFailed && state.Error != "" {
					failErr = state.Error
					break
				}
			}
			results <- streamResult{StreamID: stream.ID, Error: failErr}
			return
		case "waiting_human":
			results <- streamResult{StreamID: stream.ID, Error: "human step in sub-execution not yet supported"}
			return
		}
		// "running" — loop continues; Advance blocks internally for agent steps.
	}
}

// spawnAndMonitor spawns an agent and launches a background goroutine that waits
// for completion and publishes EventAgentCompleted or EventAgentFailed via the spawner.
func (c *Coordinator) spawnAndMonitor(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	result, err := c.spawner.Spawn(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("spawning agent: %w", err)
	}

	c.tracker.Track(result.Session, result.Process)

	go func() {
		agentResult, waitErr := result.Process.Wait()
		wasKilled := c.tracker.Finish(result.Session.ID)

		if wasKilled {
			c.spawner.MarkFailed(ctx, result.Session, "killed")
			return
		}
		if waitErr != nil || !agentResult.Success {
			errMsg := agentResult.Error
			if waitErr != nil {
				errMsg = waitErr.Error()
			}
			c.spawner.MarkFailed(ctx, result.Session, errMsg)
			return
		}
		c.spawner.MarkCompleted(ctx, result.Session, agentResult.Summary)
	}()

	return result, nil
}

func (c *Coordinator) singleStreamForObjective(ctx context.Context, objectiveID string) (*domain.Stream, error) {
	plan, err := c.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	streams, err := c.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	if len(streams) != 1 {
		return nil, nil
	}
	stream := streams[0]
	return &stream, nil
}

func (c *Coordinator) extractGeneratedMessages(step *blueprint.Step, summary string) (string, agents.GeneratedMessages) {
	if step.Messages == nil || !step.Messages.Any() {
		return summary, agents.GeneratedMessages{}
	}

	cleanSummary, msgs, found, err := agents.ExtractGeneratedMessages(summary)
	if err != nil {
		c.logger.Warn("agent produced invalid generated messages output",
			"step", step.ID,
			"error", err,
		)
		return cleanSummary, agents.GeneratedMessages{}
	}
	if !found {
		c.logger.Warn("agent did not emit generated messages output",
			"step", step.ID,
		)
		return cleanSummary, agents.GeneratedMessages{}
	}
	return cleanSummary, msgs
}

// autoCommit stages and commits all changes in a sandbox worktree.
// The commit message is taken from the agent when provided, otherwise it falls
// back to the objective description plus diff stat.
func (c *Coordinator) autoCommit(ctx context.Context, sb sandbox.Sandbox, objectiveDesc, generatedCommitMessage string) error {
	if err := c.cleanupRuntimeArtifacts(ctx, sb); err != nil {
		c.logger.Warn("failed to clean runtime artifacts before auto-commit", "error", err)
	}

	// Check if there are uncommitted changes.
	statusResult, err := sb.Exec(ctx, "git status --porcelain", sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(statusResult.Stdout) == "" {
		c.logger.Info("no uncommitted changes, skipping auto-commit")
		return nil
	}

	commitMessage := strings.TrimSpace(generatedCommitMessage)
	if commitMessage == "" {
		// Get diff stat for the commit message body.
		diffResult, _ := sb.Exec(ctx, "git diff --stat", sandbox.ExecOpts{})

		// Build commit message.
		var msg strings.Builder
		fmt.Fprintf(&msg, "tack: %s", objectiveDesc)
		if strings.TrimSpace(diffResult.Stdout) != "" {
			fmt.Fprintf(&msg, "\n\n%s", strings.TrimSpace(diffResult.Stdout))
		}
		commitMessage = msg.String()
	}

	// Stage all changes before uploading the temporary commit-message file so it
	// never becomes part of the commit.
	if stageResult, err := sb.Exec(ctx, "git add -A", sandbox.ExecOpts{}); err != nil {
		return fmt.Errorf("staging changes: %w (stderr: %s)", err, stageResult.Stderr)
	}

	const commitMessagePath = ".tack/tmp/auto-commit-message.txt"
	if err := sb.Upload(ctx, []byte(commitMessage+"\n"), commitMessagePath); err != nil {
		return fmt.Errorf("uploading commit message: %w", err)
	}
	defer func() {
		_, _ = sb.Exec(context.Background(), fmt.Sprintf("rm -f %s", naming.ShellQuote(commitMessagePath)), sandbox.ExecOpts{})
	}()

	commitCmd := fmt.Sprintf("git commit -F %s", naming.ShellQuote(commitMessagePath))
	commitResult, err := sb.Exec(ctx, commitCmd, sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("committing: %w (stderr: %s)", err, commitResult.Stderr)
	}
	if commitResult.ExitCode != 0 {
		return fmt.Errorf("git commit failed (exit %d): %s", commitResult.ExitCode, commitResult.Stderr)
	}

	c.logger.Info("auto-committed agent changes")
	return nil
}

func (c *Coordinator) cleanupRuntimeArtifacts(ctx context.Context, sb sandbox.Sandbox) error {
	res, err := sb.Exec(ctx, "git rm -r --cached --ignore-unmatch .tack-ext >/dev/null 2>&1 || true; rm -rf .tack-ext", sandbox.ExecOpts{})
	if err != nil {
		return fmt.Errorf("cleaning runtime artifacts: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("cleaning runtime artifacts exited %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}
