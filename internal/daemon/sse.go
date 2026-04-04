package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleSSE streams server-sent events to the client. Each event from the
// event bus is marshaled to JSON and written in SSE "data:" format.
func (d *Daemon) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sub, unsub := d.eventBus.Subscribe(64)
	defer unsub()
	targetProjectID := d.targetProjectID(r)

	d.logger.Info("SSE client connected", "remote", r.RemoteAddr)

	for {
		select {
		case <-r.Context().Done():
			d.logger.Info("SSE client disconnected", "remote", r.RemoteAddr)
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			if targetProjectID != "" && event.ProjectID != targetProjectID {
				continue
			}
			data, err := json.Marshal(event)
			if err != nil {
				d.logger.Error("marshaling SSE event", "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				d.logger.Error("writing SSE event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}
