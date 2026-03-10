package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
