package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/runs"
)

// handleGetRunSnapshot returns the snapshot for a run by ID.
func (d *Daemon) handleGetRunSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := d.runStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		d.logger.Error("getting run", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get run")
		return
	}
	if !d.ensureProjectMatch(w, r, run.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), run.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}

	snap, err := projectCtx.RunsService.Snapshot(r.Context(), id)
	if err != nil {
		d.logger.Error("getting run snapshot", "id", id, "error", err)
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// handleRunCommand sends an intervention (approve, retry, abort, kill) to a run.
// This is the run-centric entry point that replaces direct coordinator calls.
//
// Body: {"kind": "approve"|"retry"|"abort"|"kill", "stream_id": "...", "session_id": "...", "guidance": "...", "reason": "..."}
func (d *Daemon) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := d.runStore.Get(r.Context(), runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		d.logger.Error("loading run", "run_id", runID, "error", err)
		writeError(w, http.StatusInternalServerError, "command failed")
		return
	}
	if !d.ensureProjectMatch(w, r, run.ProjectID) {
		return
	}
	projectCtx, err := d.projectCtxs.Get(r.Context(), run.ProjectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return
	}

	var req struct {
		Kind           domain.CommandKind `json:"kind"`
		StreamID       string             `json:"stream_id"`
		SessionID      string             `json:"session_id"`
		Guidance       string             `json:"guidance"`
		Reason         string             `json:"reason"`
		ScopeAdditions []string           `json:"scope_additions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cmd := domain.Command{
		Kind:           req.Kind,
		StreamID:       req.StreamID,
		SessionID:      req.SessionID,
		Guidance:       req.Guidance,
		Reason:         req.Reason,
		ScopeAdditions: req.ScopeAdditions,
	}

	snap, err := projectCtx.RunsService.Command(r.Context(), runID, cmd)
	if err != nil {
		switch {
		case errors.Is(err, runs.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("run command", "run_id", runID, "kind", req.Kind, "error", err)
			writeError(w, http.StatusInternalServerError, "command failed")
		}
		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// handleGetObjectiveRunSnapshot returns the snapshot for the most recent run
// of an objective.
func (d *Daemon) handleGetObjectiveRunSnapshot(w http.ResponseWriter, r *http.Request) {
	objectiveID := r.PathValue("id")
	obj, err := d.objectives.Get(r.Context(), objectiveID)
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

	snap, err := projectCtx.RunsService.SnapshotByObjective(r.Context(), objectiveID)
	if err != nil {
		d.logger.Error("getting objective run snapshot", "objective_id", objectiveID, "error", err)
		writeError(w, http.StatusNotFound, "run not found for objective")
		return
	}

	writeJSON(w, http.StatusOK, snap)
}
