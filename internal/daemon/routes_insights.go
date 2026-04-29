package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
	"github.com/syndg/tack/internal/promotions"
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

func (d *Daemon) handlePromoteInsightSource(w http.ResponseWriter, r *http.Request) {
	ctx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}
	var req struct {
		Target domain.PromotionTarget `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid promotion request")
		return
	}
	service := promotions.NewService(d.candidates, d.promotions)
	record, err := service.PromoteCandidate(r.Context(), ctx.Project.ID, r.PathValue("id"), req.Target)
	if err != nil {
		d.writePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (d *Daemon) handleRejectInsightSource(w http.ResponseWriter, r *http.Request) {
	ctx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}
	service := promotions.NewService(d.candidates, d.promotions)
	record, err := service.RejectCandidate(r.Context(), ctx.Project.ID, r.PathValue("id"))
	if err != nil {
		d.writePromotionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (d *Daemon) writePromotionError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "insight source not found")
		return
	}
	if errors.Is(err, promotions.ErrWrongProject) {
		writeError(w, http.StatusNotFound, "insight source not found")
		return
	}
	if errors.Is(err, promotions.ErrInvalidTarget) {
		writeError(w, http.StatusBadRequest, "invalid promotion target")
		return
	}
	if errors.Is(err, promotions.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	d.logger.Error("mutating promotion", "error", err)
	writeError(w, http.StatusInternalServerError, "failed to mutate promotion")
}
