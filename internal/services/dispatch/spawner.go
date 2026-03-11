package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/rules"
	"github.com/syndg/deck/internal/harness/tools"
	"github.com/syndg/deck/internal/runtime"
	"github.com/syndg/deck/internal/sandbox"
	"github.com/syndg/deck/internal/services/agents"
	events "github.com/syndg/deck/internal/services/events"
)

// SpawnRequest describes what agent to create.
type SpawnRequest struct {
	Objective   *domain.Objective
	Stream      *domain.Stream // nil for planner agents
	Role        string         // "planner", "lead", "builder", "reviewer", "scout"
	TaskSpec    string         // task description or spec content
	ParentAgent string         // name of parent agent (empty for top-level)
	Guidance    string         // project-level guidance from config
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

	// 3. Provision sandbox.
	objShort := req.Objective.ID
	if len(objShort) > 8 {
		objShort = objShort[:8]
	}
	sessShort := session.ID
	if len(sessShort) > 8 {
		sessShort = sessShort[:8]
	}
	sandboxName := fmt.Sprintf("deck-%s-%s-%s", objShort, req.Role, sessShort)

	labels := map[string]string{
		"deck.objective": req.Objective.ID,
		"deck.role":      req.Role,
	}
	if req.Stream != nil {
		labels["deck.stream"] = req.Stream.ID
	}

	sb, err := s.sp.Create(ctx, sandbox.CreateOpts{
		Name:      sandboxName,
		Labels:    labels,
		Ephemeral: !role.Persistent,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioning sandbox: %w", err)
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
