package daemon

import (
	"encoding/json"
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
	if msg.ProjectID == "" {
		projectCtx, ok := d.requireProjectContext(w, r)
		if !ok {
			return
		}
		msg.ProjectID = projectCtx.Project.ID
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
