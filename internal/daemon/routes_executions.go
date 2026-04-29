package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/runs"
)

// handleListExecutions returns all blueprint executions as a JSON array.
func (d *Daemon) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	var (
		executions []blueprint.Execution
		err        error
	)
	if wantsAllProjects(r) {
		executions, err = d.executions.List(r.Context())
	} else {
		projectCtx, ok := d.requireProjectContext(w, r)
		if !ok {
			return
		}
		executions, err = d.executions.ListByProject(r.Context(), projectCtx.Project.ID)
	}
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
	if !d.ensureProjectMatch(w, r, exec.ProjectID) {
		return
	}

	writeJSON(w, http.StatusOK, exec)
}

// handleApproveExecution approves a human gate step in a blueprint execution.
// Routes through the run-centric boundary via the execution's objective.
func (d *Daemon) handleApproveExecution(w http.ResponseWriter, r *http.Request) {
	executionID := r.PathValue("id")

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
	if !d.ensureProjectMatch(w, r, exec.ProjectID) {
		return
	}

	run, err := d.runStore.GetByObjective(r.Context(), exec.ObjectiveID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no run found for execution's objective")
		return
	}

	projectCtx, err := d.projectCtxs.Get(r.Context(), exec.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}
	snap, err := projectCtx.RunsService.Act(r.Context(), run.ID, domain.Command{Kind: domain.CommandApprove})
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
}

// handleRetryExecution retries a failed stream sub-execution with optional human guidance.
// Routes through the run-centric boundary via the execution's objective.
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
	if !d.ensureProjectMatch(w, r, exec.ProjectID) {
		return
	}

	run, err := d.runStore.GetByObjective(r.Context(), exec.ObjectiveID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no run found for execution's objective")
		return
	}

	projectCtx, err := d.projectCtxs.Get(r.Context(), exec.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}
	snap, err := projectCtx.RunsService.Act(r.Context(), run.ID, domain.Command{
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
}
