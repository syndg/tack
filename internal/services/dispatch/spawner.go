package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/harness/rules"
	"github.com/syndg/deck/internal/harness/tools"
	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
	"github.com/syndg/deck/internal/services/agents"
	events "github.com/syndg/deck/internal/services/events"
)

// SpawnRequest describes what agent to create.
type SpawnRequest struct {
	Objective      *domain.Objective
	Stream         *domain.Stream             // nil for planner agents
	Role           string                     // "planner", "lead", "builder", "reviewer", "scout"
	TaskSpec       string                     // task description or spec content
	ParentAgent    string                     // name of parent agent (empty for top-level)
	Guidance       string                     // project-level guidance from config
	CommitMode     string                     // "auto", "agent", "none" — controls commit behavior
	Messages       *blueprint.MessageRequests // delivery messages the agent should generate
	ExecutionID    string                     // sub-execution ID (used to label sandbox for branch lookup)
	ReuseSandboxID string                     // if set, reuse this sandbox instead of creating a new one
	FixContext     string                     // quality gate errors from a previous fix-loop iteration
}

// SpawnResult contains the created agent session, process, and sandbox.
type SpawnResult struct {
	Session *domain.AgentSession
	Process runtime.AgentProcess
	Sandbox sandbox.Sandbox
}

// Spawner creates and manages agent processes.
type Spawner struct {
	agentStore  *db.AgentStore
	rt          runtime.AgentRuntime
	sp          sandbox.SandboxProvider
	rulesEngine *rules.Engine
	toolCurator *tools.Curator
	eventBus    *events.PersistentBus
	logger      *slog.Logger
	daemonURL   string
}

// NewSpawner creates a new Spawner.
func NewSpawner(
	agentStore *db.AgentStore,
	rt runtime.AgentRuntime,
	sp sandbox.SandboxProvider,
	rulesEngine *rules.Engine,
	toolCurator *tools.Curator,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
	daemonURL string,
) *Spawner {
	return &Spawner{
		agentStore:  agentStore,
		rt:          rt,
		sp:          sp,
		rulesEngine: rulesEngine,
		toolCurator: toolCurator,
		eventBus:    eventBus,
		logger:      logger,
		daemonURL:   daemonURL,
	}
}

// Spawn creates a sandbox, assembles the overlay, and starts an agent.
// Flow:
//  1. Look up role definition via agents.DefaultRoles()
//  2. Create AgentSession record (status "pending")
//  3. Provision sandbox
//  4. Match rules against file scope (empty for planners)
//  5. Curate tools for the role + file scope
//  6. Build agent overlay (planner vs non-planner)
//  7. Spawn agent process via runtime
//  8. Update session: status "running", sandbox ID set
//  9. Publish EventAgentSpawned
func (s *Spawner) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	// 1. Look up role definition.
	roleDefs := agents.DefaultRoles()
	role, ok := roleDefs[req.Role]
	if !ok {
		return nil, fmt.Errorf("unknown agent role: %s", req.Role)
	}

	// 2. Create AgentSession record (status "pending").
	streamID := ""
	if req.Stream != nil {
		streamID = req.Stream.ID
	}
	session := &domain.AgentSession{
		ObjectiveID: req.Objective.ID,
		StreamID:    streamID,
		Role:        domain.AgentRole(req.Role),
		Status:      "pending",
	}
	if err := s.agentStore.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("creating agent session: %w", err)
	}

	// 3. Provision sandbox (or reuse existing one for fix-loop iterations).
	objShort := req.Objective.ID
	if len(objShort) > 8 {
		objShort = objShort[:8]
	}
	sessShort := session.ID
	if len(sessShort) > 8 {
		sessShort = sessShort[:8]
	}

	var sb sandbox.Sandbox
	var err error
	if req.ReuseSandboxID != "" {
		sb, err = s.sp.Get(ctx, req.ReuseSandboxID)
		if err != nil {
			s.logger.Warn("failed to reuse sandbox, creating new one",
				"sandbox_id", req.ReuseSandboxID,
				"error", err,
			)
		}
	}
	if sb == nil {
		sandboxName := fmt.Sprintf("deck-%s-%s-%s", objShort, req.Role, sessShort)
		labels := map[string]string{
			"deck.objective": req.Objective.ID,
			"deck.role":      req.Role,
		}
		if req.Stream != nil {
			labels["deck.stream"] = req.Stream.ID
		}
		if req.ExecutionID != "" {
			labels["deck.execution"] = req.ExecutionID
		}
		sb, err = s.sp.Create(ctx, sandbox.CreateOpts{
			Name:      sandboxName,
			Labels:    labels,
			Ephemeral: !role.Persistent,
		})
		if err != nil {
			return nil, fmt.Errorf("provisioning sandbox: %w", err)
		}
	}

	// 4. Match rules against file scope (empty for planners).
	var fileScope []string
	if req.Stream != nil {
		fileScope = req.Stream.FileScope
	}
	matchedRules := s.rulesEngine.Match(fileScope)

	// 5. Curate tools for the role + file scope.
	ruleTools := make([]tools.ToolScope, 0, len(matchedRules))
	for _, mr := range matchedRules {
		if mr.Rule.Tools != nil {
			ruleTools = append(ruleTools, tools.ToolScope{
				Include: mr.Rule.Tools.Include,
				Exclude: mr.Rule.Tools.Exclude,
			})
		}
	}
	curationResult := s.toolCurator.Curate(tools.CurationInput{
		RuleTools: ruleTools,
	})
	toolNames := make([]string, len(curationResult.Tools))
	for i, t := range curationResult.Tools {
		toolNames[i] = t.Name
	}

	// 6. Build agent overlay.
	agentName := fmt.Sprintf("%s-%s-%s", req.Role, objShort, sessShort)
	var overlay string
	if req.Role == "planner" {
		overlay = agents.BuildPlannerOverlay(req.Objective, req.Guidance)
	} else {
		overlay = agents.BuildOverlay(agents.OverlayInput{
			AgentName:    agentName,
			Role:         role,
			Objective:    req.Objective,
			Stream:       req.Stream,
			TaskSpec:     req.TaskSpec,
			FileScope:    fileScope,
			MatchedRules: matchedRules,
			CuratedTools: curationResult,
			LeadAgent:    req.ParentAgent,
			Guidance:     req.Guidance,
			CommitMode:   req.CommitMode,
			Messages:     req.Messages,
			FixContext:   req.FixContext,
		})
	}

	// 7. Spawn agent process via runtime.
	agentToken := uuid.New().String()
	envVars := map[string]string{
		"DECK_DAEMON_URL":   s.daemonURL,
		"DECK_AGENT_TOKEN":  agentToken,
		"DECK_OBJECTIVE_ID": req.Objective.ID,
		"DECK_AGENT_ROLE":   req.Role,
	}
	if req.Stream != nil {
		envVars["DECK_STREAM_ID"] = req.Stream.ID
		envVars["DECK_STREAM_TITLE"] = req.Stream.Title
	}
	if req.TaskSpec != "" {
		envVars["DECK_TASK_SPEC"] = req.TaskSpec
	}

	process, err := s.rt.Spawn(ctx, sb, runtime.AgentOpts{
		Role:    req.Role,
		Overlay: overlay,
		Tools:   toolNames,
		EnvVars: envVars,
	})
	if err != nil {
		return nil, fmt.Errorf("spawning agent process: %w", err)
	}

	// 8. Update session: status "running", sandbox ID set.
	if err := s.agentStore.UpdateSandboxAndStatus(ctx, session.ID, sb.ID(), "running"); err != nil {
		s.logger.Error("failed to update session after spawn", "session_id", session.ID, "error", err)
	} else {
		session.SandboxID = sb.ID()
		session.Status = "running"
	}

	// 9. Publish EventAgentSpawned.
	payload, _ := json.Marshal(map[string]string{
		"session_id": session.ID,
		"role":       req.Role,
		"sandbox_id": sb.ID(),
	})
	s.eventBus.Publish(domain.Event{
		Type:      domain.EventAgentSpawned,
		Objective: req.Objective.ID,
		Stream:    streamID,
		Agent:     agentName,
		Payload:   string(payload),
	})

	s.logger.Info("agent spawned",
		"session_id", session.ID,
		"role", req.Role,
		"sandbox", sb.ID(),
	)

	return &SpawnResult{
		Session: session,
		Process: process,
		Sandbox: sb,
	}, nil
}

// FindSandboxForStep locates the sandbox used by a previous run of the same
// agent step. It filters by stream and role so fix-loop reuse targets the
// correct worktree instead of grabbing an unrelated sandbox from a different
// stream or role within the same objective.
func (s *Spawner) FindSandboxForStep(ctx context.Context, objectiveID, streamID, role string) (sandbox.Sandbox, error) {
	sessions, err := s.agentStore.ListByObjective(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("listing agents for objective: %s", err)
	}

	// Sessions are ordered by created_at DESC, so the first match is the
	// most recent. Filter by stream + role for a precise hit.
	for _, session := range sessions {
		if session.SandboxID == "" {
			continue
		}
		if streamID != "" && session.StreamID != streamID {
			continue
		}
		if role != "" && string(session.Role) != role {
			continue
		}
		found, err := s.sp.Get(ctx, session.SandboxID)
		if err != nil {
			continue
		}
		return found, nil
	}
	return nil, fmt.Errorf("no sandbox found for objective %s stream %s role %s", objectiveID, streamID, role)
}

// MarkCompleted updates an agent session to "completed" and publishes EventAgentCompleted.
func (s *Spawner) MarkCompleted(ctx context.Context, session *domain.AgentSession, summary string) {
	if err := s.agentStore.UpdateStatus(ctx, session.ID, "completed"); err != nil {
		s.logger.Error("failed to update agent session to completed", "session_id", session.ID, "error", err)
	}
	payload, _ := json.Marshal(map[string]string{
		"session_id": session.ID,
		"summary":    summary,
	})
	s.eventBus.Publish(domain.Event{
		Type:      domain.EventAgentCompleted,
		Objective: session.ObjectiveID,
		Stream:    session.StreamID,
		Agent:     session.ID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})
	s.logger.Info("agent completed", "session_id", session.ID)
}

// MarkFailed updates an agent session to "failed" and publishes EventAgentFailed.
func (s *Spawner) MarkFailed(ctx context.Context, session *domain.AgentSession, reason string) {
	if err := s.agentStore.UpdateStatus(ctx, session.ID, "failed"); err != nil {
		s.logger.Error("failed to update agent session to failed", "session_id", session.ID, "error", err)
	}
	payload, _ := json.Marshal(map[string]string{
		"session_id": session.ID,
		"reason":     reason,
	})
	s.eventBus.Publish(domain.Event{
		Type:      domain.EventAgentFailed,
		Objective: session.ObjectiveID,
		Stream:    session.StreamID,
		Agent:     session.ID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})
	s.logger.Info("agent failed", "session_id", session.ID, "reason", reason)
}

// Kill terminates an agent process and updates its session status.
// Marks session as "failed" and publishes EventAgentFailed.
func (s *Spawner) Kill(ctx context.Context, sessionID string) error {
	session, err := s.agentStore.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("getting agent session %s: %w", sessionID, err)
	}

	if err := s.agentStore.UpdateStatus(ctx, sessionID, "failed"); err != nil {
		return fmt.Errorf("marking session failed: %w", err)
	}

	payload, _ := json.Marshal(map[string]string{
		"session_id": sessionID,
		"reason":     "killed",
	})
	s.eventBus.Publish(domain.Event{
		Type:      domain.EventAgentFailed,
		Objective: session.ObjectiveID,
		Stream:    session.StreamID,
		Agent:     sessionID,
		Payload:   string(payload),
	})

	s.logger.Info("agent killed", "session_id", sessionID)
	return nil
}
