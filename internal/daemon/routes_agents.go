package daemon

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/services/runs"
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

// handleKillAgent terminates an active agent session.
// Routes through the run-centric boundary via the agent's objective.
func (d *Daemon) handleKillAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	session, err := d.agents.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent session not found")
			return
		}
		d.logger.Error("loading agent session for kill", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to kill agent")
		return
	}

	run, err := d.runStore.GetByObjective(r.Context(), session.ObjectiveID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no run found for agent's objective")
		return
	}

	snap, err := d.runsService.Command(r.Context(), run.ID, domain.Command{
		Kind:      domain.CommandKill,
		SessionID: id,
	})
	if err != nil {
		switch {
		case errors.Is(err, runs.ErrInvalidState):
			writeError(w, http.StatusConflict, err.Error())
		default:
			d.logger.Error("killing agent via run", "session_id", id, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to kill agent")
		}
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
