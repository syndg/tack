package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/credentials"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
	"github.com/syndg/tack/internal/naming"
	"github.com/syndg/tack/internal/runtime"
	"github.com/syndg/tack/internal/sandbox"
	"github.com/syndg/tack/internal/services/agents"
	events "github.com/syndg/tack/internal/services/events"
)

// SpawnRequest describes what agent to create.
type SpawnRequest struct {
	Objective   *domain.Objective
	Stream      *domain.Stream             // nil for planner agents
	Role        string                     // "planner", "lead", "builder", "reviewer", "scout"
	TaskSpec    string                     // task description or spec content
	ParentAgent string                     // name of parent agent (empty for top-level)
	Guidance    string                     // project-level guidance from config
	CommitMode  string                     // "auto", "agent", "none" — controls commit behavior
	Messages    *blueprint.MessageRequests // delivery messages the agent should generate
	ExecutionID string                     // sub-execution ID (used to label sandbox for branch lookup)
	FixContext  string                     // quality gate errors from a previous fix-loop iteration
}

// SpawnResult contains the created agent session, process, and sandbox.
type SpawnResult struct {
	Session *domain.AgentSession
	Process runtime.AgentProcess
	Sandbox sandbox.Sandbox
}

// providerEnvVars maps model provider names to the env var used to inject the key.
var providerEnvVars = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"gemini":    "GEMINI_API_KEY",
	"groq":      "GROQ_API_KEY",
	"mistral":   "MISTRAL_API_KEY",
	"xai":       "XAI_API_KEY",
}

// gitHostEnvVars maps git host names to the env var used to inject the token.
var gitHostEnvVars = map[string]string{
	"github.com": "GITHUB_TOKEN",
	"gitlab.com": "GITLAB_TOKEN",
}

// Spawner creates and manages agent processes.
type Spawner struct {
	agentStore  *db.AgentStore
	rt          runtime.AgentRuntime
	sp          sandbox.SandboxProvider
	rulesEngine *rules.Engine
	toolCurator *tools.Curator
	eventBus    *events.PersistentBus
	creds       *credentials.Store
	provider    string // model provider name (e.g., "anthropic")
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
	creds *credentials.Store,
	provider string,
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
		creds:       creds,
		provider:    provider,
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

	// 3. Provision sandbox: one sandbox per stream, reused across agents.
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

	if req.Stream != nil {
		// Stream agent (scout/builder/reviewer): reuse existing stream sandbox.
		existing, listErr := s.sp.List(ctx, map[string]string{
			"tack.stream": req.Stream.ID,
		})
		if listErr == nil && len(existing) > 0 {
			sb = existing[0]
			s.logger.Info("reusing stream sandbox",
				"sandbox_id", sb.ID(),
				"stream", req.Stream.ID,
				"role", req.Role,
			)
		}
	}

	if sb == nil {
		// Create new sandbox with stream-based or planner naming.
		var sandboxName, branch string
		labels := map[string]string{
			"tack.objective": req.Objective.ID,
		}
		if req.Stream != nil {
			slug := naming.StreamSlug(req.Stream.Title)
			sandboxName = fmt.Sprintf("tack-%s-%s", objShort, slug)
			branch = fmt.Sprintf("tack/%s/%s", objShort, slug)
			labels["tack.stream"] = req.Stream.ID
		} else {
			// Planner or other non-stream agent.
			sandboxName = fmt.Sprintf("tack-%s-%s", objShort, req.Role)
			branch = fmt.Sprintf("tack/%s/%s", objShort, req.Role)
		}
		if req.ExecutionID != "" {
			labels["tack.execution"] = req.ExecutionID
		}
		sb, err = s.sp.Create(ctx, sandbox.CreateOpts{
			Name:      sandboxName,
			Branch:    branch,
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
			ruleTools = append(ruleTools, *mr.Rule.Tools)
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
		"DECK_AGENT_NAME":   agentName,
		"DECK_OBJECTIVE_ID": req.Objective.ID,
		"DECK_AGENT_ROLE":   req.Role,
	}
	if req.Stream != nil {
		envVars["DECK_STREAM_ID"] = req.Stream.ID
		envVars["DECK_STREAM_TITLE"] = req.Stream.Title
		if len(req.Stream.FileScope) > 0 {
			envVars["DECK_FILE_SCOPE"] = strings.Join(req.Stream.FileScope, ",")
		}
	}
	if req.TaskSpec != "" {
		envVars["DECK_TASK_SPEC"] = req.TaskSpec
	}

	// Inject model provider credential (only the configured provider).
	s.injectCredentials(envVars)

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
	s.eventBus.Emit(domain.EventAgentSpawned, req.Objective.ID, streamID, agentName,
		"session_id", session.ID,
		"role", req.Role,
		"sandbox_id", sb.ID(),
	)

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
	s.eventBus.Emit(domain.EventAgentCompleted, session.ObjectiveID, session.StreamID, session.ID,
		"session_id", session.ID,
		"summary", summary,
	)
	s.logger.Info("agent completed", "session_id", session.ID)
}

// MarkFailed updates an agent session to "failed" and publishes EventAgentFailed.
func (s *Spawner) MarkFailed(ctx context.Context, session *domain.AgentSession, reason string) {
	if err := s.agentStore.UpdateStatus(ctx, session.ID, "failed"); err != nil {
		s.logger.Error("failed to update agent session to failed", "session_id", session.ID, "error", err)
	}
	s.eventBus.Emit(domain.EventAgentFailed, session.ObjectiveID, session.StreamID, session.ID,
		"session_id", session.ID,
		"reason", reason,
	)
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

	s.eventBus.Emit(domain.EventAgentFailed, session.ObjectiveID, session.StreamID, sessionID,
		"session_id", sessionID,
		"reason", "killed",
	)

	s.logger.Info("agent killed", "session_id", sessionID)
	return nil
}

// injectCredentials adds model provider and git credentials to the env map.
func (s *Spawner) injectCredentials(envVars map[string]string) {
	if s.creds == nil {
		return
	}

	// Model provider: only inject the one matching the configured provider.
	if s.provider != "" {
		resolved, err := s.creds.ModelProvider(s.provider)
		if err != nil {
			s.logger.Warn("credential injection: model provider not found", "provider", s.provider, "error", err)
		} else {
			envName, ok := providerEnvVars[s.provider]
			if ok {
				envVars[envName] = resolved.Value
			} else {
				s.logger.Warn("credential injection: no env var mapping for provider", "provider", s.provider)
			}
		}
	}

	// Git token: always inject if configured.
	host := s.creds.GitHost()
	if host != "" {
		tok, err := s.creds.GitToken("")
		if err != nil {
			s.logger.Warn("credential injection: git token resolution failed", "error", err)
		} else {
			envName, ok := gitHostEnvVars[host]
			if !ok {
				envName = "GIT_TOKEN" // fallback for unknown hosts
			}
			envVars[envName] = tok
		}
	}
}

// GetSandbox retrieves a sandbox by ID from the provider.
func (s *Spawner) GetSandbox(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	return s.sp.Get(ctx, sandboxID)
}

// DeleteSandbox deletes a single sandbox by ID.
func (s *Spawner) DeleteSandbox(ctx context.Context, sandboxID string) error {
	return s.sp.Delete(ctx, sandboxID)
}

// CleanupObjective deletes all sandboxes (worktrees + branches) for an objective.
// Called when the objective reaches a terminal state (completed, partial, failed).
func (s *Spawner) CleanupObjective(ctx context.Context, objectiveID string) {
	// Find all sandboxes for this objective
	sandboxes, err := s.sp.List(ctx, map[string]string{
		"tack.objective": objectiveID,
	})
	if err != nil {
		s.logger.Error("listing sandboxes for cleanup", "objective", objectiveID, "error", err)
		return
	}

	if len(sandboxes) == 0 {
		return
	}

	deleted := 0
	for _, sb := range sandboxes {
		if err := s.sp.Delete(ctx, sb.ID()); err != nil {
			s.logger.Warn("failed to delete sandbox", "sandbox_id", sb.ID(), "objective", objectiveID, "error", err)
			continue
		}
		deleted++
	}

	s.logger.Info("cleaned up objective sandboxes",
		"objective", objectiveID,
		"deleted", deleted,
		"total", len(sandboxes),
	)
}
