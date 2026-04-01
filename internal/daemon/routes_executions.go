package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/dispatch"
	"github.com/syndg/tack/internal/services/runs"
)

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

// handleApproveExecution approves a human gate step in a blueprint execution.
// Routes through the run-centric boundary when a Run exists for the execution's
// objective; falls back to the coordinator for executions that predate the runs API.
func (d *Daemon) handleApproveExecution(w http.ResponseWriter, r *http.Request) {
	executionID := r.PathValue("id")

	// Try to route through the run boundary.
	exec, err := d.executions.Get(r.Context(), executionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		d.logger.Error("loading execution for approve", "execution_id", executionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to approve execution")
		return
	}

	if run, err := d.runStore.GetByObjective(r.Context(), exec.ObjectiveID); err == nil {
		// Run exists — route through the run-centric boundary.
		snap, err := d.runsService.Command(r.Context(), run.ID, domain.Command{Kind: domain.CommandApprove})
		if err != nil {
			switch {
			case errors.Is(err, runs.ErrInvalidState):
				writeError(w, http.StatusConflict, err.Error())
			default:
				d.logger.Error("approving execution via run", "execution_id", executionID, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to approve execution")
			}
			return
		}
		writeJSON(w, http.StatusOK, snap)
		return
	}

	// No run — fall back to direct coordinator call (pre-migration path).
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}
	if err := d.coordinator.Approve(r.Context(), executionID); err != nil {
		switch {
		case errors.Is(err, dispatch.ErrNotFound):
			writeError(w, http.StatusNotFound, "execution not found")
		case errors.Is(err, dispatch.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("approving execution", "execution_id", executionID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to approve execution")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "execution_id": executionID})
}

// handleRetryExecution retries a failed stream sub-execution with optional human guidance.
// Routes through the run-centric boundary when a Run exists; falls back to the
// coordinator for executions that predate the runs API.
// Body: {"guidance": "..."}  (optional)
func (d *Daemon) handleRetryExecution(w http.ResponseWriter, r *http.Request) {
	executionID := r.PathValue("id")

	var req struct {
		Guidance string `json:"guidance"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	exec, err := d.executions.Get(r.Context(), executionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		d.logger.Error("loading execution for retry", "execution_id", executionID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry execution")
		return
	}

	if run, err := d.runStore.GetByObjective(r.Context(), exec.ObjectiveID); err == nil {
		// Run exists — route through the run-centric boundary.
		snap, err := d.runsService.Command(r.Context(), run.ID, domain.Command{
			Kind:     domain.CommandRetry,
			StreamID: exec.StreamID,
			Guidance: req.Guidance,
		})
		if err != nil {
			switch {
			case errors.Is(err, runs.ErrInvalidState):
				writeError(w, http.StatusConflict, err.Error())
			default:
				d.logger.Error("retrying execution via run", "execution_id", executionID, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to retry execution")
			}
			return
		}
		writeJSON(w, http.StatusAccepted, snap)
		return
	}

	// No run — fall back to direct coordinator call (pre-migration path).
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}
	if err := d.coordinator.Retry(r.Context(), executionID, req.Guidance); err != nil {
		switch {
		case errors.Is(err, dispatch.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, dispatch.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("retrying stream execution", "execution_id", executionID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to retry execution")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "execution_id": executionID})
}
