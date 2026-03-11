package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	events "github.com/syndg/deck/internal/services/events"
	"github.com/syndg/deck/internal/services/lifecycle"
)

// Coordinator orchestrates objective execution from plan approval to completion.
// It subscribes to events and drives blueprint execution.
type Coordinator struct {
	engine     *blueprint.Engine
	scheduler  *Scheduler
	spawner    *Spawner
	lifecycle  *lifecycle.Manager
	executions *db.ExecutionStore
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	eventBus   *events.PersistentBus
	logger     *slog.Logger

	mu          sync.Mutex
	activeExecs map[string]context.CancelFunc // objectiveID → cancel
	agentMap    map[string]*SpawnResult        // sessionID → spawn result
}

// NewCoordinator creates a new Coordinator and registers step handlers with the engine.
func NewCoordinator(
	engine *blueprint.Engine,
	scheduler *Scheduler,
	spawner *Spawner,
	lc *lifecycle.Manager,
	executions *db.ExecutionStore,
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Coordinator {
	c := &Coordinator{
		engine:      engine,
		scheduler:   scheduler,
		spawner:     spawner,
		lifecycle:   lc,
		executions:  executions,
		objectives:  objectives,
		plans:       plans,
		streams:     streams,
		eventBus:    eventBus,
		logger:      logger,
		activeExecs: make(map[string]context.CancelFunc),
		agentMap:    make(map[string]*SpawnResult),
	}

	// Register step handlers with the engine.
	engine.RegisterHandler(blueprint.StepTypeAgent, c.HandleAgentStep)
	engine.RegisterHandler(blueprint.StepTypeBlueprintRef, c.HandleBlueprintRefStep)

	return c
}

// Start subscribes to events and begins processing.
// Subscribes to:
//
//	EventObjectiveUpdated — detect objective transitions to "approved" → start execution
//	EventStreamReady — spawn lead agents for newly unblocked streams
//
// Runs event processing in a background goroutine.
func (c *Coordinator) Start(ctx context.Context) error {
	sub, unsub := c.eventBus.Subscribe(128)

	go func() {
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub:
				if !ok {
					return
				}
				switch event.Type {
				case domain.EventObjectiveUpdated:
					c.handleObjectiveUpdated(ctx, event)
				case domain.EventStreamReady:
					c.handleStreamReady(ctx, event)
				}
			}
		}
	}()

	c.logger.Info("coordinator started")
	return nil
}

// Stop cancels all active executions and cleans up.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for objectiveID, cancel := range c.activeExecs {
		c.logger.Info("cancelling active execution on stop", "objective_id", objectiveID)
		cancel()
	}
	c.activeExecs = make(map[string]context.CancelFunc)
	c.logger.Info("coordinator stopped")
}

// handleObjectiveUpdated processes EventObjectiveUpdated events.
// Triggers StartExecution when an objective transitions to "approved".
func (c *Coordinator) handleObjectiveUpdated(ctx context.Context, event domain.Event) {
	var payload map[string]string
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		c.logger.Error("failed to parse objective updated payload", "error", err)
		return
	}

	if payload["to"] != string(domain.ObjectiveStatusApproved) {
		return
	}

	if event.Objective == "" {
		return
	}

	if err := c.StartExecution(ctx, event.Objective); err != nil {
		c.logger.Error("failed to start execution after approval",
			"objective_id", event.Objective,
			"error", err,
		)
	}
}

// handleStreamReady processes EventStreamReady by spawning a lead agent
// for the newly unblocked stream (dispatched by the dispatch_streams step).
// Only spawns if the stream is still "pending" to prevent double-spawn with
// HandleBlueprintRefStep.
func (c *Coordinator) handleStreamReady(ctx context.Context, event domain.Event) {
	var payload map[string]string
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		c.logger.Error("failed to parse stream ready payload", "error", err)
		return
	}

	streamID := payload["stream_id"]
	if streamID == "" {
		return
	}

	stream, err := c.streams.Get(ctx, streamID)
	if err != nil {
		c.logger.Error("failed to get stream", "stream_id", streamID, "error", err)
		return
	}

	// Only spawn for pending streams; skip if already claimed by HandleBlueprintRefStep.
	if stream.Status != "pending" {
		return
	}

	plan, err := c.plans.Get(ctx, stream.PlanID)
	if err != nil {
		c.logger.Error("failed to get plan for stream", "plan_id", stream.PlanID, "error", err)
		return
	}

	obj, err := c.objectives.Get(ctx, plan.ObjectiveID)
	if err != nil {
		c.logger.Error("failed to get objective", "objective_id", plan.ObjectiveID, "error", err)
		return
	}

	// Claim the stream before spawning to prevent double-spawn.
	if err := c.scheduler.MarkExecuting(ctx, streamID); err != nil {
		c.logger.Error("failed to mark stream executing", "stream_id", streamID, "error", err)
		return
	}

	if _, err := c.spawnAndMonitor(ctx, SpawnRequest{
		Objective: obj,
		Stream:    stream,
		Role:      "lead",
		TaskSpec:  stream.Description,
	}); err != nil {
		c.logger.Error("failed to spawn lead for stream",
			"stream_id", streamID,
			"error", err,
		)
	}
}

// StartExecution begins blueprint execution for an approved objective.
//  1. Fetch objective, verify status is "approved"
//  2. Fetch plan for the objective
//  3. Determine blueprint name (from objective.Blueprint, default to "feature")
//  4. Create execution via engine.Start(blueprintName, objectiveID)
//  5. Transition objective to "executing" via lifecycle
//  6. Run the execution loop in a goroutine
func (c *Coordinator) StartExecution(ctx context.Context, objectiveID string) error {
	// 1. Fetch objective and verify status is "approved".
	obj, err := c.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective %s: %w", objectiveID, err)
	}
	if obj.Status != domain.ObjectiveStatusApproved {
		return fmt.Errorf("objective %s is not approved (status: %s)", objectiveID, obj.Status)
	}

	// 2. Fetch plan for the objective.
	if _, err := c.plans.GetByObjective(ctx, objectiveID); err != nil {
		return fmt.Errorf("getting plan for objective %s: %w", objectiveID, err)
	}

	// 3. Determine blueprint name, defaulting to "feature".
	blueprintName := obj.Blueprint
	if blueprintName == "" {
		blueprintName = "feature"
	}

	// 4. Create execution via engine.Start.
	exec, err := c.engine.Start(ctx, blueprintName, objectiveID)
	if err != nil {
		return fmt.Errorf("starting blueprint execution for objective %s: %w", objectiveID, err)
	}
	if err := c.executions.Create(ctx, exec); err != nil {
		return fmt.Errorf("persisting execution for objective %s: %w", objectiveID, err)
	}

	// 5. Transition objective to "executing".
	if err := c.lifecycle.Transition(ctx, objectiveID, domain.ObjectiveStatusExecuting); err != nil {
		return fmt.Errorf("transitioning objective %s to executing: %w", objectiveID, err)
	}

	// Publish EventExecutionStarted.
	evtPayload, _ := json.Marshal(map[string]string{
		"execution_id":   exec.ID,
		"blueprint_name": blueprintName,
	})
	c.eventBus.Publish(domain.Event{
		Type:      domain.EventExecutionStarted,
		Objective: objectiveID,
		Payload:   string(evtPayload),
		CreatedAt: time.Now(),
	})

	// 6. Run the execution loop in a goroutine.
	execCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.activeExecs[objectiveID] = cancel
	c.mu.Unlock()

	go c.runExecution(execCtx, exec)

	c.logger.Info("execution started",
		"execution_id", exec.ID,
		"objective_id", objectiveID,
		"blueprint", blueprintName,
	)
	return nil
}

// runExecution drives the blueprint execution loop.
// Calls engine.Advance() repeatedly until the execution reaches a terminal state.
func (c *Coordinator) runExecution(ctx context.Context, exec *blueprint.Execution) {
	defer func() {
		c.mu.Lock()
		delete(c.activeExecs, exec.ObjectiveID)
		c.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			c.logger.Info("execution cancelled",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		}

		updated, err := c.engine.Advance(ctx, exec)
		if err != nil {
			c.logger.Error("engine advance failed",
				"execution_id", exec.ID,
				"error", err,
			)
			return
		}
		exec = updated

		// Persist state after each advance.
		if err := c.executions.Update(ctx, exec); err != nil {
			c.logger.Error("failed to persist execution state",
				"execution_id", exec.ID,
				"error", err,
			)
		}

		switch exec.Status {
		case "completed":
			c.logger.Info("execution completed",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		case "failed":
			c.logger.Error("execution failed",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		case "waiting_human":
			// Execution paused; resumes when engine.ApproveHuman is called.
			c.logger.Info("execution waiting for human approval",
				"execution_id", exec.ID,
				"objective_id", exec.ObjectiveID,
			)
			return
		}
	}
}

// HandleAgentStep implements the StepHandler for agent-type blueprint steps.
// Registered with the engine as the StepTypeAgent handler.
//  1. Determine role from step.Role
//  2. Build SpawnRequest (objective, role, task spec)
//  3. Call spawner.Spawn() to create sandbox + agent
//  4. Track the spawn result in agentMap
//  5. Call process.Wait() — blocks until agent completes
//  6. Update agent session status (completed or failed)
//  7. Publish EventAgentCompleted or EventAgentFailed
//  8. Return StepResult based on agent result
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

	// Spawn the agent.
	result, err := c.spawner.Spawn(ctx, SpawnRequest{
		Objective: obj,
		Role:      role,
		TaskSpec:  step.Description,
	})
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("spawning agent for step %q: %s", step.ID, err),
		}, nil
	}

	// Track the spawn result.
	c.mu.Lock()
	c.agentMap[result.Session.ID] = result
	c.mu.Unlock()

	c.logger.Info("agent spawned for step",
		"session_id", result.Session.ID,
		"role", role,
		"step", step.ID,
		"execution_id", exec.ID,
	)

	// Block until agent completes.
	agentResult, waitErr := result.Process.Wait()

	// Clean up tracking.
	c.mu.Lock()
	delete(c.agentMap, result.Session.ID)
	c.mu.Unlock()

	if waitErr != nil || !agentResult.Success {
		errMsg := agentResult.Error
		if waitErr != nil {
			errMsg = waitErr.Error()
		}
		c.spawner.MarkFailed(ctx, result.Session, errMsg)
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("agent step %q failed: %s", step.ID, errMsg),
		}, nil
	}

	c.spawner.MarkCompleted(ctx, result.Session, agentResult.Summary)
	return blueprint.StepResult{
		Status: blueprint.StepStatusCompleted,
		Output: agentResult.Summary,
	}, nil
}

// HandleBlueprintRefStep implements the StepHandler for blueprint_ref steps.
// Registered with the engine as the StepTypeBlueprintRef handler.
// For Phase 4, this handles per-stream execution without nested blueprints:
//  1. Get the plan and all its streams
//  2. Spawn lead agents for all ready streams (via spawner)
//  3. Monitor agent completions via event subscription
//  4. As leads complete: mark stream completed via scheduler, spawn newly ready leads
//  5. Block until ALL streams are completed
//  6. Return StepResult{Status: "completed"}
func (c *Coordinator) HandleBlueprintRefStep(ctx context.Context, exec *blueprint.Execution, step *blueprint.Step) (blueprint.StepResult, error) {
	// 1. Get the plan and all its streams.
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

	// Build a set of stream IDs for event filtering and pre-count already completed.
	streamSet := make(map[string]bool, totalStreams)
	completedCount := 0
	for _, s := range allStreams {
		streamSet[s.ID] = true
		if s.Status == "completed" {
			completedCount++
		}
	}

	if completedCount == totalStreams {
		return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
	}

	obj, err := c.objectives.Get(ctx, exec.ObjectiveID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting objective: %s", err),
		}, nil
	}

	// Subscribe to events BEFORE spawning to avoid missing completions.
	sub, unsub := c.eventBus.Subscribe(128)
	defer unsub()

	// 2. Spawn lead agents for all initially ready streams.
	ready, err := c.scheduler.GetReadyStreams(ctx, plan.ID)
	if err != nil {
		return blueprint.StepResult{
			Status: blueprint.StepStatusFailed,
			Error:  fmt.Sprintf("getting ready streams: %s", err),
		}, nil
	}

	for i := range ready {
		stream := ready[i]
		if err := c.scheduler.MarkExecuting(ctx, stream.ID); err != nil {
			c.logger.Warn("failed to mark stream executing",
				"stream_id", stream.ID,
				"error", err,
			)
			continue
		}
		if _, err := c.spawnAndMonitor(ctx, SpawnRequest{
			Objective: obj,
			Stream:    &stream,
			Role:      "lead",
			TaskSpec:  stream.Description,
		}); err != nil {
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  fmt.Sprintf("spawning lead for stream %s: %s", stream.ID, err),
			}, nil
		}
	}

	// 4. Wait for events until all streams complete or one fails.
	for completedCount < totalStreams {
		select {
		case <-ctx.Done():
			return blueprint.StepResult{
				Status: blueprint.StepStatusFailed,
				Error:  "execution context cancelled",
			}, nil

		case event, ok := <-sub:
			if !ok {
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  "event subscription closed unexpectedly",
				}, nil
			}

			switch event.Type {
			case domain.EventAgentCompleted:
				if !streamSet[event.Stream] {
					continue // Not one of our streams.
				}
				if err := c.scheduler.MarkCompleted(ctx, event.Stream, plan.ID); err != nil {
					c.logger.Error("failed to mark stream completed",
						"stream_id", event.Stream,
						"error", err,
					)
				}
				completedCount++
				c.logger.Info("stream agent completed",
					"stream_id", event.Stream,
					"completed", completedCount,
					"total", totalStreams,
				)

			case domain.EventAgentFailed:
				if !streamSet[event.Stream] {
					continue
				}
				if err := c.scheduler.MarkFailed(ctx, event.Stream); err != nil {
					c.logger.Error("failed to mark stream failed",
						"stream_id", event.Stream,
						"error", err,
					)
				}
				var failPayload map[string]string
				_ = json.Unmarshal([]byte(event.Payload), &failPayload)
				return blueprint.StepResult{
					Status: blueprint.StepStatusFailed,
					Error:  fmt.Sprintf("stream %s agent failed: %s", event.Stream, failPayload["reason"]),
				}, nil

			case domain.EventStreamReady:
				// A previously blocked stream is now ready — spawn a lead for it.
				var readyPayload map[string]string
				if err := json.Unmarshal([]byte(event.Payload), &readyPayload); err != nil {
					continue
				}
				streamID := readyPayload["stream_id"]
				if !streamSet[streamID] {
					continue // Not our plan.
				}

				stream, err := c.streams.Get(ctx, streamID)
				if err != nil {
					c.logger.Error("failed to get cascade stream", "stream_id", streamID, "error", err)
					continue
				}

				// Only spawn if pending; skip if already claimed by Start()'s handler.
				if stream.Status != "pending" {
					continue
				}

				if err := c.scheduler.MarkExecuting(ctx, streamID); err != nil {
					c.logger.Warn("failed to mark cascade stream executing",
						"stream_id", streamID,
						"error", err,
					)
					continue
				}
				if _, err := c.spawnAndMonitor(ctx, SpawnRequest{
					Objective: obj,
					Stream:    stream,
					Role:      "lead",
					TaskSpec:  stream.Description,
				}); err != nil {
					c.logger.Error("failed to spawn lead for cascade stream",
						"stream_id", streamID,
						"error", err,
					)
				}
			}
		}
	}

	// 6. All streams completed.
	return blueprint.StepResult{Status: blueprint.StepStatusCompleted}, nil
}

// spawnAndMonitor spawns an agent and launches a background goroutine that waits
// for completion and publishes EventAgentCompleted or EventAgentFailed via the spawner.
func (c *Coordinator) spawnAndMonitor(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	result, err := c.spawner.Spawn(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("spawning agent: %w", err)
	}

	c.mu.Lock()
	c.agentMap[result.Session.ID] = result
	c.mu.Unlock()

	go func() {
		agentResult, waitErr := result.Process.Wait()

		c.mu.Lock()
		delete(c.agentMap, result.Session.ID)
		c.mu.Unlock()

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
