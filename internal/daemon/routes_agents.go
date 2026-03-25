package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
)

// handleListAgents returns all agent sessions as a JSON array.
func (d *Daemon) handleListAgents(w http.ResponseWriter, r *http.Request) {
	sessions, err := d.agents.List(r.Context())
	if err != nil {
		d.logger.Error("listing agent sessions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	if sessions == nil {
		sessions = []domain.AgentSession{}
	}

	writeJSON(w, http.StatusOK, sessions)
}

// handleGetAgent returns a single agent session by ID.
func (d *Daemon) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	session, err := d.agents.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent session not found")
			return
		}
		d.logger.Error("getting agent session", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get agent session")
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// handleKillAgent terminates an active agent session via the coordinator.
func (d *Daemon) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	if d.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "coordinator not available")
		return
	}

	id := r.PathValue("id")

	if err := d.coordinator.Kill(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent session not found")
			return
		}
		d.logger.Error("killing agent session", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to kill agent")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
