package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

// handleSendMail sends a mail message (used by agent extensions).
// Body: {"from", "to", "subject", "body", "type", "priority?", "thread_id?", "payload?", "objective", "stream?"}
func (d *Daemon) handleSendMail(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	var msg domain.MailMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	projectCtx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}
	if claims := agentClaimsFromRequest(r); claims != nil {
		if msg.From == "" {
			msg.From = claims.AgentName
		} else if msg.From != claims.AgentName {
			writeError(w, http.StatusForbidden, "agent may not spoof sender")
			return
		}
		if msg.Objective == "" {
			msg.Objective = claims.ObjectiveID
		} else if msg.Objective != claims.ObjectiveID {
			writeError(w, http.StatusForbidden, "agent may not target another objective")
			return
		}
		if claims.StreamID != "" {
			if msg.Stream == "" {
				msg.Stream = claims.StreamID
			} else if msg.Stream != claims.StreamID {
				writeError(w, http.StatusForbidden, "agent may not target another stream")
				return
			}
		}
	}
	if msg.ProjectID == "" {
		msg.ProjectID = projectCtx.Project.ID
	} else if msg.ProjectID != projectCtx.Project.ID {
		writeError(w, http.StatusNotFound, "resource not found in targeted project")
		return
	}
	if msg.Objective != "" {
		obj, err := d.objectives.Get(r.Context(), msg.Objective)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "objective not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to validate objective")
			return
		}
		if obj.ProjectID != msg.ProjectID {
			writeError(w, http.StatusNotFound, "resource not found in targeted project")
			return
		}
	}

	if err := d.mailBroker.Send(r.Context(), &msg); err != nil {
		d.logger.Error("sending mail", "from", msg.From, "to", msg.To, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send mail")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": msg.ID})
}

// handleListMail returns messages matching query params (debug/dashboard).
// GET /mail?objective={id}&from={name}&to={name}&type={type}&unread=true
func (d *Daemon) handleListMail(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	q := r.URL.Query()
	filters := db.MailFilters{
		ProjectID:  d.targetProjectID(r),
		Objective:  q.Get("objective"),
		From:       q.Get("from"),
		To:         q.Get("to"),
		Type:       q.Get("type"),
		UnreadOnly: q.Get("unread") == "true",
	}
	if filters.ProjectID == "" && !wantsAllProjects(r) {
		projectCtx, ok := d.requireProjectContext(w, r)
		if !ok {
			return
		}
		filters.ProjectID = projectCtx.Project.ID
	}

	messages, err := d.mailBroker.List(r.Context(), filters)
	if err != nil {
		d.logger.Error("listing mail", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list mail")
		return
	}

	if messages == nil {
		messages = []domain.MailMessage{}
	}

	writeJSON(w, http.StatusOK, messages)
}

// handleGetUnreadMail returns unread messages for an agent.
func (d *Daemon) handleGetUnreadMail(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	agentName := r.PathValue("agentName")
	projectCtx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}

	messages, err := d.mailBroker.GetUnread(r.Context(), agentName, projectCtx.Project.ID)
	if err != nil {
		d.logger.Error("getting unread mail", "agent", agentName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get unread mail")
		return
	}

	if messages == nil {
		messages = []domain.MailMessage{}
	}

	writeJSON(w, http.StatusOK, messages)
}

// handleMarkMailRead marks a single message as read by its numeric ID.
func (d *Daemon) handleMarkMailRead(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message ID")
		return
	}
	msg, err := d.mail.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load message")
		return
	}
	if !d.ensureProjectMatch(w, r, msg.ProjectID) {
		return
	}

	if err := d.mailBroker.MarkRead(r.Context(), id); err != nil {
		d.logger.Error("marking mail read", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to mark message read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMarkAllMailRead marks all unread messages for an agent as read.
func (d *Daemon) handleMarkAllMailRead(w http.ResponseWriter, r *http.Request) {
	if d.mailBroker == nil {
		writeError(w, http.StatusServiceUnavailable, "mail broker not available")
		return
	}

	agentName := r.PathValue("agentName")
	projectCtx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}

	if err := d.mailBroker.MarkAllRead(r.Context(), agentName, projectCtx.Project.ID); err != nil {
		d.logger.Error("marking all mail read", "agent", agentName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to mark all messages read")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
