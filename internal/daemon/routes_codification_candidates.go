package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
)

func (d *Daemon) handleListObjectiveCodificationCandidates(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective for codification candidates", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
		return
	}
	if !d.ensureProjectMatch(w, r, obj.ProjectID) {
		return
	}
	candidates, err := d.candidates.ListByObjective(r.Context(), id)
	if err != nil {
		d.logger.Error("listing codification candidates", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list codification candidates")
		return
	}
	if candidates == nil {
		candidates = []domain.CodificationCandidate{}
	}
	writeJSON(w, http.StatusOK, candidates)
}
