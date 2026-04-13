package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/syndg/tack/internal/domain"
)

func (d *Daemon) handleGetObjectiveDossier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective for dossier", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
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
	dossier, err := projectCtx.DiscoveryService.GetDossier(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "dossier not found")
			return
		}
		d.logger.Error("getting dossier", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get dossier")
		return
	}
	writeJSON(w, http.StatusOK, dossier)
}

func (d *Daemon) handleUpdateObjectiveDossier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective for dossier update", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
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
	var dossier domain.Dossier
	if err := json.NewDecoder(r.Body).Decode(&dossier); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	updated, err := projectCtx.DiscoveryService.UpdateDossier(r.Context(), id, dossier)
	if err != nil {
		d.logger.Error("updating dossier", "objective_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update dossier")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
