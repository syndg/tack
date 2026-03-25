package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/dispatch"
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
func (d *Daemon) handleApproveExecution(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")
	if err := d.coordinator.Approve(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, dispatch.ErrNotFound):
			writeError(w, http.StatusNotFound, "execution not found")
		case errors.Is(err, dispatch.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("approving execution", "execution_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to approve execution")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "execution_id": id})
}

// handleRetryExecution retries a failed stream sub-execution with optional human guidance.
// Body: {"guidance": "..."}  (optional)
func (d *Daemon) handleRetryExecution(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")

	var req struct {
		Guidance string `json:"guidance"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := d.coordinator.Retry(r.Context(), id, req.Guidance); err != nil {
		switch {
		case errors.Is(err, dispatch.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, dispatch.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("retrying stream execution", "execution_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to retry execution")
		}
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "execution_id": id})
}
