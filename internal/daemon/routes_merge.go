package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/observability"
)

// handleListMergeQueue returns merge queue entries as a JSON array.
// If ?objective={id} is provided, filters by objective. Otherwise returns all entries.
func (d *Daemon) handleListMergeQueue(w http.ResponseWriter, r *http.Request) {
	objectiveID := r.URL.Query().Get("objective")

	var entries []domain.MergeEntry
	var err error

	if objectiveID != "" {
		entries, err = d.mergeQueue.ListByObjective(r.Context(), objectiveID)
	} else if wantsAllProjects(r) {
		entries, err = d.mergeQueue.ListAll(r.Context())
	} else {
		projectCtx, ok := d.requireProjectContext(w, r)
		if !ok {
			return
		}
		entries, err = d.mergeQueue.ListByProject(r.Context(), projectCtx.Project.ID)
	}
	if err != nil {
		d.logger.Error("listing merge queue", "objective", objectiveID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list merge queue")
		return
	}

	if entries == nil {
		entries = []domain.MergeEntry{}
	}

	writeJSON(w, http.StatusOK, entries)
}

// handleGetMergeEntry returns a single merge queue entry by ID.
func (d *Daemon) handleGetMergeEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, err := d.mergeQueue.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "merge entry not found")
			return
		}
		d.logger.Error("getting merge entry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}
	if !d.ensureProjectMatch(w, r, entry.ProjectID) {
		return
	}

	writeJSON(w, http.StatusOK, entry)
}

// handleRetryMerge resets a failed or conflicted merge entry back to pending.
func (d *Daemon) handleRetryMerge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	entry, err := d.mergeQueue.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "merge entry not found")
			return
		}
		d.logger.Error("getting merge entry for retry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}
	if !d.ensureProjectMatch(w, r, entry.ProjectID) {
		return
	}

	if entry.Status != domain.MergeStatusFailed && entry.Status != domain.MergeStatusConflict {
		writeError(w, http.StatusConflict, "only failed or conflict entries can be retried")
		return
	}

	stream, err := d.streams.Get(r.Context(), entry.StreamID)
	if err != nil {
		d.logger.Error("getting stream for merge retry", "stream_id", entry.StreamID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry merge entry")
		return
	}
	if stream.Status == domain.StreamStatusFailed {
		if err := d.streams.UpdateStatus(r.Context(), entry.StreamID, domain.StreamStatusMergeReady); err != nil {
			d.logger.Error("resetting stream for merge retry", "stream_id", entry.StreamID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to retry merge entry")
			return
		}
	}

	if err := d.mergeQueue.UpdateStatus(r.Context(), id, domain.MergeStatusPending, 0, "", ""); err != nil {
		d.logger.Error("retrying merge entry", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry merge entry")
		return
	}

	if d.observability != nil {
		d.observability.RecordMilestone(observability.Milestone{
			EventType:   domain.EventMergeQueued,
			ProjectID:   entry.ProjectID,
			ObjectiveID: entry.ObjectiveID,
			StreamID:    entry.StreamID,
			Status:      "queued",
			Details: map[string]any{
				"entry_id":     entry.ID,
				"stream_id":    entry.StreamID,
				"plan_id":      entry.PlanID,
				"objective_id": entry.ObjectiveID,
				"branch":       entry.Branch,
			},
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetStreamDiff returns the diff summary for a merged stream.
func (d *Daemon) handleGetStreamDiff(w http.ResponseWriter, r *http.Request) {
	streamID := r.PathValue("id")

	entry, err := d.mergeQueue.GetByStream(r.Context(), streamID)
	if err == nil && !d.ensureProjectMatch(w, r, entry.ProjectID) {
		return
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no merge entry found for stream")
			return
		}
		d.logger.Error("getting merge entry for stream diff", "stream_id", streamID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get merge entry")
		return
	}

	if entry.DiffStat == "" {
		writeError(w, http.StatusNotFound, "no diff available for this stream")
		return
	}

	// diff_stat is already a JSON string — write it directly to avoid double-encoding.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(entry.DiffStat)); err != nil {
		d.logger.Warn("writing diff stat response", "error", err)
	}
}
