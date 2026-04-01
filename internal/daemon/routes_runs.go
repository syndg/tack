package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/runs"
)

// handleGetRunSnapshot returns the snapshot for a run by ID.
func (d *Daemon) handleGetRunSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	snap, err := d.runsService.Snapshot(r.Context(), id)
	if err != nil {
		d.logger.Error("getting run snapshot", "id", id, "error", err)
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// handleRunCommand sends an intervention (approve, retry, abort) to a run.
// This is the run-centric entry point that replaces direct coordinator calls.
//
// Body: {"kind": "approve"|"retry"|"abort", "stream_id": "...", "guidance": "...", "reason": "..."}
func (d *Daemon) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	if d.runsService == nil {
		writeError(w, http.StatusServiceUnavailable, "runs service not available")
		return
	}

	runID := r.PathValue("id")

	var req struct {
		Kind     domain.CommandKind `json:"kind"`
		StreamID string             `json:"stream_id"`
		Guidance string             `json:"guidance"`
		Reason   string             `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cmd := domain.Command{
		Kind:     req.Kind,
		StreamID: req.StreamID,
		Guidance: req.Guidance,
		Reason:   req.Reason,
	}

	snap, err := d.runsService.Command(r.Context(), runID, cmd)
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

	snap, err := d.runsService.SnapshotByObjective(r.Context(), objectiveID)
	if err != nil {
		d.logger.Error("getting objective run snapshot", "objective_id", objectiveID, "error", err)
		writeError(w, http.StatusNotFound, "run not found for objective")
		return
	}

	writeJSON(w, http.StatusOK, snap)
}
