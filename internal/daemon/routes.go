package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/syndg/deck/internal/harness/blueprint"
)

// registerRoutes sets up all HTTP route handlers on the daemon's mux.
func (d *Daemon) registerRoutes() {
	// Objectives
	d.mux.HandleFunc("POST /objectives", d.handleCreateObjective)
	d.mux.HandleFunc("GET /objectives/{id}", d.handleGetObjective)
	d.mux.HandleFunc("GET /objectives", d.handleListObjectives)
	d.mux.HandleFunc("GET /objectives/{id}/plan", d.handleGetObjectivePlan)
	d.mux.HandleFunc("POST /objectives/{id}/execute", d.handleExecuteObjective)

	// Plans & Streams
	d.mux.HandleFunc("POST /plans", d.handleCreatePlan)
	d.mux.HandleFunc("GET /plans", d.handleListPlans)
	d.mux.HandleFunc("GET /plans/{id}", d.handleGetPlan)
	d.mux.HandleFunc("POST /plans/{id}/approve", d.handleApprovePlan)
	d.mux.HandleFunc("POST /plans/{id}/reject", d.handleRejectPlan)
	d.mux.HandleFunc("GET /plans/{id}/streams", d.handleListStreams)
	d.mux.HandleFunc("GET /streams/{id}", d.handleGetStream)

	// Executions
	d.mux.HandleFunc("GET /executions", d.handleListExecutions)
	d.mux.HandleFunc("GET /executions/{id}", d.handleGetExecution)
	d.mux.HandleFunc("POST /executions/{id}/approve", d.handleApproveExecution)
	d.mux.HandleFunc("POST /executions/{id}/retry", d.handleRetryExecution)

	// Agents
	d.mux.HandleFunc("GET /agents", d.handleListAgents)
	d.mux.HandleFunc("GET /agents/{id}", d.handleGetAgent)
	d.mux.HandleFunc("POST /agents/{id}/kill", d.handleKillAgent)

	// Mail
	d.mux.HandleFunc("POST /mail", d.handleSendMail)
	d.mux.HandleFunc("GET /mail", d.handleListMail)
	d.mux.HandleFunc("GET /mail/{agentName}/unread", d.handleGetUnreadMail)
	d.mux.HandleFunc("POST /mail/{id}/read", d.handleMarkMailRead)
	d.mux.HandleFunc("POST /mail/{agentName}/read-all", d.handleMarkAllMailRead)

	// Merge Queue
	d.mux.HandleFunc("GET /merge-queue", d.handleListMergeQueue)
	d.mux.HandleFunc("GET /merge-queue/{id}", d.handleGetMergeEntry)
	d.mux.HandleFunc("POST /merge-queue/{id}/retry", d.handleRetryMerge)
	d.mux.HandleFunc("GET /streams/{id}/diff", d.handleGetStreamDiff)

	// Blueprints & System
	d.mux.HandleFunc("GET /blueprints", d.handleListBlueprints)
	d.mux.HandleFunc("GET /blueprints/{name}", d.handleGetBlueprint)
	d.mux.HandleFunc("GET /events", d.handleSSE)
	d.mux.HandleFunc("GET /health", d.handleHealth)
	d.mux.HandleFunc("GET /status", d.handleStatus)
}

// handleHealth responds with a simple health check.
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// StatusResponse is the JSON response for the /status endpoint.
type StatusResponse struct {
	Status     string         `json:"status"`
	Uptime     string         `json:"uptime"`
	Objectives map[string]int `json:"objectives"`
}

// handleStatus responds with daemon status including uptime and objective counts by status.
func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	objectives, err := d.objectives.List(r.Context())
	if err != nil {
		d.logger.Error("listing objectives for status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get status")
		return
	}

	counts := make(map[string]int)
	for _, obj := range objectives {
		counts[string(obj.Status)]++
	}

	resp := StatusResponse{
		Status:     "ok",
		Uptime:     time.Since(d.startTime).Round(time.Second).String(),
		Objectives: counts,
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleListBlueprints returns all available blueprints as a JSON array.
func (d *Daemon) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	names := d.blueprintRegistry.List()
	blueprints := make([]blueprint.Blueprint, 0, len(names))
	for _, name := range names {
		bp, ok := d.blueprintRegistry.Get(name)
		if ok {
			blueprints = append(blueprints, *bp)
		}
	}
	writeJSON(w, http.StatusOK, blueprints)
}

// handleGetBlueprint returns a specific blueprint by name.
func (d *Daemon) handleGetBlueprint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	bp, ok := d.blueprintRegistry.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "blueprint not found")
		return
	}

	writeJSON(w, http.StatusOK, bp)
}

// writeJSON marshals data to JSON and writes it to the response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("encoding JSON response", "status", status, "error", err)
	}
}

// writeError writes a JSON error response with the given status code and message.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		slog.Error("encoding JSON error response", "status", status, "error", err)
	}
}
