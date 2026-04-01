package daemon

import (
	"net/http"
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
