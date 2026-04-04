package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/runs"
)

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
// and streams. Delegates to planningService.CreatePlan for orchestration.
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

	obj, err := d.objectives.Get(r.Context(), req.ObjectiveID)
	if err != nil {
		writeError(w, http.StatusNotFound, "objective not found")
		return
	}
	if !d.ensureProjectMatch(w, r, obj.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), obj.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}
	plan, err := projectCtx.PlanningService.CreatePlan(r.Context(), req.ObjectiveID, req.Output)
	if err != nil {
		if strings.Contains(err.Error(), "parsing plan") || strings.Contains(err.Error(), "validating plan") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		d.logger.Error("creating plan", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}

	writeJSON(w, http.StatusCreated, plan)
}

// handleListPlans returns all plans as a JSON array.
func (d *Daemon) handleListPlans(w http.ResponseWriter, r *http.Request) {
	var (
		plans []domain.Plan
		err   error
	)
	if wantsAllProjects(r) {
		plans, err = d.plans.List(r.Context())
	} else {
		projectCtx, ok := d.requireProjectContext(w, r)
		if !ok {
			return
		}
		plans, err = d.plans.ListByProject(r.Context(), projectCtx.Project.ID)
	}
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
	planMeta, err := d.plans.Get(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("getting plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}
	if !d.ensureProjectMatch(w, r, planMeta.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), planMeta.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}

	plan, streams, err := projectCtx.PlanningService.GetPlanWithStreams(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("getting plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}
	if streams == nil {
		streams = []domain.Stream{}
	}

	writeJSON(w, http.StatusOK, planWithStreams{Plan: plan, Streams: streams})
}

// handleApprovePlan approves a plan through the planning workflow boundary.
func (d *Daemon) handleApprovePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	planMeta, err := d.plans.Get(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to approve plan")
		return
	}
	if !d.ensureProjectMatch(w, r, planMeta.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), planMeta.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}

	plan, err := projectCtx.PlanningService.ApprovePlan(r.Context(), id)
	if err != nil {
		switch {
		case isPlanNotFound(err):
			writeError(w, http.StatusNotFound, "plan not found")
		case errors.Is(err, runs.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("approving plan", "id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to approve plan")
		}
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// handleRejectPlan rejects a plan and returns the objective to planning.
func (d *Daemon) handleRejectPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	planMeta, err := d.plans.Get(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reject plan")
		return
	}
	if !d.ensureProjectMatch(w, r, planMeta.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), planMeta.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}

	plan, err := projectCtx.PlanningService.RejectPlan(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("rejecting plan", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reject plan")
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// handleListStreams returns all streams for a plan.
func (d *Daemon) handleListStreams(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("id")

	plan, err := d.plans.Get(r.Context(), planID)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		d.logger.Error("getting plan for stream list", "plan_id", planID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}
	if !d.ensureProjectMatch(w, r, plan.ProjectID) {
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
	if !d.ensureProjectMatch(w, r, stream.ProjectID) {
		return
	}

	writeJSON(w, http.StatusOK, stream)
}
