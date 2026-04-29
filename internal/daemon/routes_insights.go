package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
)

func (d *Daemon) handleGetObjectiveInsightReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective for insight report", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
		return
	}
	if !d.ensureProjectMatch(w, r, obj.ProjectID) {
		return
	}
	insights, err := d.insights.ListByObjective(r.Context(), id, 0)
	if err != nil {
		d.logger.Error("listing objective insights", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list objective insights")
		return
	}
	candidates, err := d.candidates.ListByObjective(r.Context(), id)
	if err != nil {
		d.logger.Error("listing codification candidates", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list codification candidates")
		return
	}
	if insights == nil {
		insights = []domain.ObjectiveInsight{}
	}
	if candidates == nil {
		candidates = []domain.CodificationCandidate{}
	}
	writeJSON(w, http.StatusOK, insightreport.Build(id, insights, candidates))
}
