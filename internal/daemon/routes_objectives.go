package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/runs"
)

// CreateObjectiveRequest is the JSON body for POST /objectives.
type CreateObjectiveRequest struct {
	Description string `json:"description"`
	Blueprint   string `json:"blueprint,omitempty"`
}

// handleCreateObjective decodes a JSON body, creates a new objective, publishes
// an event, and responds with the created objective. The selected blueprint is
// either provided explicitly on the objective or resolved by the coordinator
// from the loaded blueprint registry.
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

	obj := &domain.Objective{
		Description: req.Description,
		Blueprint:   req.Blueprint,
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

	// Start execution through the run-centric boundary. The event above
	// is informational (SSE); execution is driven by runsService.Start().
	if _, err := d.runsService.Start(r.Context(), obj.ID); err != nil {
		d.logger.Error("starting run for objective", "objective_id", obj.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "objective created but failed to start execution")
		return
	}

	refreshedObj, err := d.objectives.Get(r.Context(), obj.ID)
	if err != nil {
		d.logger.Error("refreshing objective after start", "objective_id", obj.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to refresh objective after start")
		return
	}

	writeJSON(w, http.StatusCreated, refreshedObj)
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

// handleGetObjectivePlan returns the plan for a given objective along with its streams.
func (d *Daemon) handleGetObjectivePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	plan, streams, err := d.planningService.GetPlanByObjective(r.Context(), id)
	if err != nil {
		if isPlanNotFound(err) {
			writeError(w, http.StatusNotFound, "plan not found for objective")
			return
		}
		d.logger.Error("getting plan by objective", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get plan")
		return
	}
	if streams == nil {
		streams = []domain.Stream{}
	}

	writeJSON(w, http.StatusOK, planWithStreams{Plan: plan, Streams: streams})
}

// handleExecuteObjective triggers blueprint execution for an objective through
// the run-centric orchestration boundary. Creates a durable Run record and
// returns its initial snapshot.
func (d *Daemon) handleExecuteObjective(w http.ResponseWriter, r *http.Request) {
	if d.runsService == nil {
		writeError(w, http.StatusServiceUnavailable, "runs service not available")
		return
	}

	id := r.PathValue("id")
	snap, err := d.runsService.Start(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, dispatch.ErrNotFound):
			writeError(w, http.StatusNotFound, "objective not found")
		case errors.Is(err, dispatch.ErrInvalidState), errors.Is(err, runs.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("starting run", "objective_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to start execution")
		}
		return
	}

	writeJSON(w, http.StatusAccepted, snap)
}
