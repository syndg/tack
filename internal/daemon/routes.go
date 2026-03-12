package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/services/planner"
)

// registerRoutes sets up all HTTP route handlers on the daemon's mux.
func (d *Daemon) registerRoutes() {
	d.mux.HandleFunc("POST /objectives", d.handleCreateObjective)
	d.mux.HandleFunc("GET /objectives/{id}", d.handleGetObjective)
	d.mux.HandleFunc("GET /objectives", d.handleListObjectives)
	d.mux.HandleFunc("GET /objectives/{id}/plan", d.handleGetObjectivePlan)
	d.mux.HandleFunc("GET /events", d.handleSSE)
	d.mux.HandleFunc("GET /health", d.handleHealth)
	d.mux.HandleFunc("GET /status", d.handleStatus)

	d.mux.HandleFunc("GET /blueprints", d.handleListBlueprints)
	d.mux.HandleFunc("GET /blueprints/{name}", d.handleGetBlueprint)
	d.mux.HandleFunc("GET /executions", d.handleListExecutions)
	d.mux.HandleFunc("GET /executions/{id}", d.handleGetExecution)

	d.mux.HandleFunc("POST /plans", d.handleCreatePlan)
	d.mux.HandleFunc("GET /plans", d.handleListPlans)
	d.mux.HandleFunc("GET /plans/{id}", d.handleGetPlan)
	d.mux.HandleFunc("POST /plans/{id}/approve", d.handleApprovePlan)
	d.mux.HandleFunc("POST /plans/{id}/reject", d.handleRejectPlan)
	d.mux.HandleFunc("GET /plans/{id}/streams", d.handleListStreams)
	d.mux.HandleFunc("GET /streams/{id}", d.handleGetStream)

	d.mux.HandleFunc("POST /mail", d.handleSendMail)
	d.mux.HandleFunc("GET /mail/{agentName}/unread", d.handleGetUnreadMail)
	d.mux.HandleFunc("POST /mail/{id}/read", d.handleMarkMailRead)
	d.mux.HandleFunc("POST /mail/{agentName}/read-all", d.handleMarkAllMailRead)

	d.mux.HandleFunc("POST /executions/{id}/approve", d.handleApproveExecution)
	d.mux.HandleFunc("GET /agents", d.handleListAgents)
	d.mux.HandleFunc("GET /agents/{id}", d.handleGetAgent)
	d.mux.HandleFunc("POST /agents/{id}/kill", d.handleKillAgent)
	d.mux.HandleFunc("POST /objectives/{id}/execute", d.handleExecuteObjective)

	d.mux.HandleFunc("GET /merge-queue", d.handleListMergeQueue)
	d.mux.HandleFunc("GET /merge-queue/{id}", d.handleGetMergeEntry)
	d.mux.HandleFunc("POST /merge-queue/{id}/retry", d.handleRetryMerge)
	d.mux.HandleFunc("GET /streams/{id}/diff", d.handleGetStreamDiff)
}

// CreateObjectiveRequest is the JSON body for POST /objectives.
type CreateObjectiveRequest struct {
	Description string `json:"description"`
	Blueprint   string `json:"blueprint,omitempty"`
	Simple      bool   `json:"simple,omitempty"` // single-agent mode: creates and auto-approves a plan
	Auto        bool   `json:"auto,omitempty"`   // batch planning mode: planner runs autonomously
}

// createObjectiveSimpleResponse is the response for POST /objectives when simple=true.
type createObjectiveSimpleResponse struct {
	Objective *domain.Objective `json:"objective"`
	Plan      *domain.Plan      `json:"plan"`
}

// handleCreateObjective decodes a JSON body, creates a new objective, publishes
// an event, and responds with the created objective.
//
// When simple=true: calls planningService.StartSimple to create and auto-approve
// a single-stream plan, returning {"objective": {...}, "plan": {...}}.
//
// When auto=true: creates the objective normally; batch planning mode is recorded
// and a planner agent will be spawned asynchronously (Phase 4).
func (d *Daemon) handleCreateObjective(w http.ResponseWriter, r *http.Request) {
	var req CreateObjectiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	if req.Simple {
		obj, plan, err := d.planningService.StartSimple(r.Context(), req.Description, planner.SimpleOpts{
			Blueprint:   req.Blueprint,
			AutoApprove: true,
		})
		if err != nil {
			d.logger.Error("starting simple mode", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to start simple mode")
			return
		}
		writeJSON(w, http.StatusCreated, createObjectiveSimpleResponse{
			Objective: obj,
			Plan:      plan,
		})
		return
	}

	planningMode := ""
	if req.Auto {
		planningMode = "batch"
	}

	obj := &domain.Objective{
		Description:  req.Description,
		Blueprint:    req.Blueprint,
		PlanningMode: planningMode,
	}
	if err := d.objectives.Create(r.Context(), obj); err != nil {
		d.logger.Error("creating objective", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create objective")
		return
	}

	d.eventBus.Publish(domain.Event{
		Type:      domain.EventObjectiveCreated,
		Objective: obj.ID,
		Payload:   obj.Description,
		CreatedAt: time.Now(),
	})

	writeJSON(w, http.StatusCreated, obj)
}

// handleGetObjective retrieves a single objective by ID from the path.
func (d *Daemon) handleGetObjective(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
		return
	}

	writeJSON(w, http.StatusOK, obj)
}

// handleListObjectives returns all objectives as a JSON array.
func (d *Daemon) handleListObjectives(w http.ResponseWriter, r *http.Request) {
	objectives, err := d.objectives.List(r.Context())
	if err != nil {
		d.logger.Error("listing objectives", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list objectives")
		return
	}

	if objectives == nil {
		objectives = []domain.Objective{}
	}

	writeJSON(w, http.StatusOK, objectives)
}

// handleHealth responds with a simple health check.
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// StatusResponse is the JSON response for the /status endpoint.
type StatusResponse struct {
	Status     string         `json:"status"`
	Uptime     string         `json:"uptime"`
	Objectives map[string]int `json:"objectives"`
}

// handleStatus responds with daemon status including uptime and objective counts by status.
func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	objectives, err := d.objectives.List(r.Context())
	if err != nil {
		d.logger.Error("listing objectives for status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get status")
		return
	}

	counts := make(map[string]int)
	for _, obj := range objectives {
		counts[string(obj.Status)]++
	}

	resp := StatusResponse{
		Status:     "ok",
		Uptime:     time.Since(d.startTime).Round(time.Second).String(),
		Objectives: counts,
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleListBlueprints returns all available blueprints as a JSON array.
func (d *Daemon) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	names := d.blueprintRegistry.List()
	blueprints := make([]blueprint.Blueprint, 0, len(names))
	for _, name := range names {
		bp, ok := d.blueprintRegistry.Get(name)
		if ok {
			blueprints = append(blueprints, *bp)
		}
	}
	writeJSON(w, http.StatusOK, blueprints)
}

// handleGetBlueprint returns a specific blueprint by name.
func (d *Daemon) handleGetBlueprint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	bp, ok := d.blueprintRegistry.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "blueprint not found")
		return
	}

	writeJSON(w, http.StatusOK, bp)
}

// handleListExecutions returns all blueprint executions as a JSON array.
func (d *Daemon) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	executions, err := d.executions.List(r.Context())
	if err != nil {
		d.logger.Error("listing executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list executions")
		return
	}

	if executions == nil {
		executions = []blueprint.Execution{}
	}

	writeJSON(w, http.StatusOK, executions)
}

// handleGetExecution returns a specific execution by ID.
func (d *Daemon) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	exec, err := d.executions.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		d.logger.Error("getting execution", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return
	}

	writeJSON(w, http.StatusOK, exec)
}

// planWithStreams is the response shape for plan endpoints that include streams.
type planWithStreams struct {
	Plan    *domain.Plan    `json:"plan"`
	Streams []domain.Stream `json:"streams"`
}

// isPlanNotFound reports whether an error from PlanStore or StreamStore indicates
// a missing record (the stores return descriptive errors, not sql.ErrNoRows).
func isPlanNotFound(err error) bool {
	return strings.Contains(err.Error(), "not found")
}

// handleCreatePlan parses raw planner-agent output and persists the resulting plan
// and streams. Intended for internal use by the planning service.
func (d *Daemon) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ObjectiveID string `json:"objective_id"`
		Output      string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ObjectiveID == "" {
		writeError(w, http.StatusBadRequest, "objective_id is required")
		return
	}
	if req.Output == "" {
		writeError(w, http.StatusBadRequest, "output is required")
		return
	}

	rawPlan, err := planner.ParsePlan(req.Output)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse plan: "+err.Error())
		return
	}
	if err := planner.ValidatePlan(rawPlan); err != nil {
		writeError(w, http.StatusBadRequest, "invalid plan: "+err.Error())
		return
	}

	plan, streams := planner.ToDomain(rawPlan, req.ObjectiveID)

	if err := d.plans.Create(r.Context(), plan); err != nil {
		d.logger.Error("creating plan", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	for i := range streams {
		if err := d.streams.Create(r.Context(), &streams[i]); err != nil {
			d.logger.Error("creating stream", "title", streams[i].Title, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create stream")
			return
		}
	}

	d.eventBus.Publish(domain.Event{
		Type:      domain.EventPlanCreated,
		Objective: req.ObjectiveID,
		Payload:   plan.ID,
		CreatedAt: time.Now(),
	})

	if err := d.lifecycleManager.MarkPlanReady(r.Context(), plan.ID); err != nil {
		d.logger.Error("marking plan ready", "plan_id", plan.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to mark plan ready")
		return
	}

	updated, err := d.plans.Get(r.Context(), plan.ID)
	if err != nil {
		d.logger.Error("refreshing plan", "plan_id", plan.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to refresh plan")
		return
	}

	writeJSON(w, http.StatusCreated, updated)
}

// handleListPlans returns all plans as a JSON array.
func (d *Daemon) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := d.plans.List(r.Context())
	if err != nil {
		d.logger.Error("listing plans", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	if plans == nil {
		plans = []domain.Plan{}
	}
	writeJSON(w, http.StatusOK, plans)
}

// handleGetPlan returns a plan together with its streams.
func (d *Daemon) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	plan, err := d.plans.Get(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("getting plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}

	streams, err := d.streams.ListByPlan(r.Context(), id)
	if err != nil {
		d.logger.Error("listing streams for plan", "plan_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list streams")
		return
	}
	if streams == nil {
		streams = []domain.Stream{}
	}

	writeJSON(w, http.StatusOK, planWithStreams{Plan: plan, Streams: streams})
}

// handleApprovePlan approves a plan for execution via the lifecycle manager.
func (d *Daemon) handleApprovePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := d.lifecycleManager.ApprovePlan(r.Context(), id); err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("approving plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to approve plan")
		return
	}

	plan, err := d.plans.Get(r.Context(), id)
	if err != nil {
		d.logger.Error("refreshing plan after approve", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to refresh plan")
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// handleRejectPlan rejects a plan and returns the objective to planning.
func (d *Daemon) handleRejectPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := d.lifecycleManager.RejectPlan(r.Context(), id); err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("rejecting plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reject plan")
		return
	}

	plan, err := d.plans.Get(r.Context(), id)
	if err != nil {
		d.logger.Error("refreshing plan after reject", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to refresh plan")
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// handleGetObjectivePlan returns the plan for a given objective along with its streams.
func (d *Daemon) handleGetObjectivePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	plan, err := d.plans.GetByObjective(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found for objective")
			return
		}
		d.logger.Error("getting plan by objective", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}

	streams, err := d.streams.ListByPlan(r.Context(), plan.ID)
	if err != nil {
		d.logger.Error("listing streams for objective plan", "plan_id", plan.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list streams")
		return
	}
	if streams == nil {
		streams = []domain.Stream{}
	}

	writeJSON(w, http.StatusOK, planWithStreams{Plan: plan, Streams: streams})
}

// handleListStreams returns all streams for a plan.
func (d *Daemon) handleListStreams(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("id")

	// Verify the plan exists before listing streams.
	if _, err := d.plans.Get(r.Context(), planID); err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("getting plan for stream list", "plan_id", planID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}

	streams, err := d.streams.ListByPlan(r.Context(), planID)
	if err != nil {
		d.logger.Error("listing streams", "plan_id", planID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list streams")
		return
	}
	if streams == nil {
		streams = []domain.Stream{}
	}

	writeJSON(w, http.StatusOK, streams)
}

// handleGetStream returns a single stream by ID.
func (d *Daemon) handleGetStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	stream, err := d.streams.Get(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "stream not found")
			return
		}
		d.logger.Error("getting stream", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get stream")
		return
	}

	writeJSON(w, http.StatusOK, stream)
}

// handleSendMail sends a mail message (used by agent extensions).
// Body: {"from": "...", "to": "...", "type": "...", "payload": "...", "objective": "...", "stream": "..."}
// If "to" starts with "@", broker.Send delegates to SendBroadcast internally.
func (d *Daemon) handleSendMail(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	var msg domain.MailMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := d.mailBroker.Send(r.Context(), &msg); err != nil {
		d.logger.Error("sending mail", "from", msg.From, "to", msg.To, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send mail")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetUnreadMail returns unread messages for an agent as a JSON array.
func (d *Daemon) handleGetUnreadMail(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	agentName := r.PathValue("agentName")

	messages, err := d.mailBroker.GetUnread(r.Context(), agentName)
	if err != nil {
		d.logger.Error("getting unread mail", "agent", agentName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get unread mail")
		return
	}

	if messages == nil {
		messages = []domain.MailMessage{}
	}

	writeJSON(w, http.StatusOK, messages)
}

// handleMarkMailRead marks a single message as read by its numeric ID.
func (d *Daemon) handleMarkMailRead(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message ID")
		return
	}

	if err := d.mailBroker.MarkRead(r.Context(), id); err != nil {
		d.logger.Error("marking mail read", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to mark message read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMarkAllMailRead marks all unread messages for an agent as read.
func (d *Daemon) handleMarkAllMailRead(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	agentName := r.PathValue("agentName")

	if err := d.mailBroker.MarkAllRead(r.Context(), agentName); err != nil {
		d.logger.Error("marking all mail read", "agent", agentName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to mark all messages read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleApproveExecution approves a human gate step in a blueprint execution.
// Calls engine.ApproveHuman to mark the step completed, persists the state, and
// triggers the coordinator to resume the execution loop.
func (d *Daemon) handleApproveExecution(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")

	exec, err := d.executions.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		d.logger.Error("getting execution for approve", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return
	}

	exec, err = d.blueprintEngine.ApproveHuman(r.Context(), exec)
	if err != nil {
		d.logger.Error("approving human step", "execution_id", id, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := d.executions.Update(r.Context(), exec); err != nil {
		d.logger.Error("updating execution after approval", "execution_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to persist execution")
		return
	}

	d.coordinator.ResumeExecution(exec)
	writeJSON(w, http.StatusOK, exec)
}

// handleListAgents returns all agent sessions as a JSON array.
func (d *Daemon) handleListAgents(w http.ResponseWriter, r *http.Request) {
	sessions, err := d.agents.List(r.Context())
	if err != nil {
		d.logger.Error("listing agent sessions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	if sessions == nil {
		sessions = []domain.AgentSession{}
	}

	writeJSON(w, http.StatusOK, sessions)
}

// handleGetAgent returns a single agent session by ID.
func (d *Daemon) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	session, err := d.agents.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent session not found")
			return
		}
		d.logger.Error("getting agent session", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get agent session")
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// handleKillAgent terminates an active agent session via the coordinator.
func (d *Daemon) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")

	if err := d.coordinator.KillAgent(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent session not found")
			return
		}
		d.logger.Error("killing agent session", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to kill agent")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExecuteObjective manually triggers blueprint execution for an approved objective.
// Verifies the objective exists and is in "approved" status before delegating to
// coordinator.StartExecution.
func (d *Daemon) handleExecuteObjective(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")

	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective for execute", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
		return
	}

	if obj.Status != domain.ObjectiveStatusApproved {
		writeError(w, http.StatusConflict, "objective is not in approved status")
		return
	}

	if err := d.coordinator.StartExecution(r.Context(), id); err != nil {
		d.logger.Error("starting execution", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start execution")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "executing", "objective_id": id})
}

// handleListMergeQueue returns merge queue entries as a JSON array.
// If ?objective={id} is provided, filters by objective. Otherwise returns all pending entries.
func (d *Daemon) handleListMergeQueue(w http.ResponseWriter, r *http.Request) {
	objectiveID := r.URL.Query().Get("objective")

	var entries []domain.MergeEntry
	var err error

	if objectiveID != "" {
		entries, err = d.mergeQueueStore.ListByObjective(r.Context(), objectiveID)
	} else {
		entries, err = d.mergeQueueStore.ListPending(r.Context())
	}
	if err != nil {
		d.logger.Error("listing merge queue", "objective", objectiveID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list merge queue")
		return
	}

	if entries == nil {
		entries = []domain.MergeEntry{}
	}

	writeJSON(w, http.StatusOK, entries)
}

// handleGetMergeEntry returns a single merge queue entry by ID.
func (d *Daemon) handleGetMergeEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, err := d.mergeQueueStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "merge entry not found")
			return
		}
		d.logger.Error("getting merge entry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}

	writeJSON(w, http.StatusOK, entry)
}

// handleRetryMerge resets a failed or conflicted merge entry back to pending.
func (d *Daemon) handleRetryMerge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, err := d.mergeQueueStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "merge entry not found")
			return
		}
		d.logger.Error("getting merge entry for retry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}

	if entry.Status != domain.MergeStatusFailed && entry.Status != domain.MergeStatusConflict {
		writeError(w, http.StatusConflict, "only failed or conflict entries can be retried")
		return
	}

	if err := d.mergeQueueStore.UpdateStatus(r.Context(), id, domain.MergeStatusPending, 0, "", ""); err != nil {
		d.logger.Error("retrying merge entry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry merge entry")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetStreamDiff returns the diff summary for a merged stream.
// Reads the DiffSummary stored in the merge entry's diff_stat field.
func (d *Daemon) handleGetStreamDiff(w http.ResponseWriter, r *http.Request) {
	streamID := r.PathValue("id")

	entry, err := d.mergeQueueStore.GetByStream(r.Context(), streamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no merge entry found for stream")
			return
		}
		d.logger.Error("getting merge entry for stream diff", "stream_id", streamID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}

	if entry.DiffStat == "" {
		writeError(w, http.StatusNotFound, "no diff available for this stream")
		return
	}

	// diff_stat is already a JSON string — write it directly to avoid double-encoding.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(entry.DiffStat))
}

// writeJSON marshals data to JSON and writes it to the response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("encoding JSON response", "status", status, "error", err)
	}
}

// writeError writes a JSON error response with the given status code and message.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		slog.Error("encoding JSON error response", "status", status, "error", err)
	}
}
