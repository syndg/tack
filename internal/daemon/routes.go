package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
)

// registerRoutes sets up all HTTP route handlers on the daemon's mux.
func (d *Daemon) registerRoutes() {
	d.mux.HandleFunc("POST /objectives", d.handleCreateObjective)
	d.mux.HandleFunc("GET /objectives/{id}", d.handleGetObjective)
	d.mux.HandleFunc("GET /objectives", d.handleListObjectives)
	d.mux.HandleFunc("GET /events", d.handleSSE)
	d.mux.HandleFunc("GET /health", d.handleHealth)
	d.mux.HandleFunc("GET /status", d.handleStatus)

	d.mux.HandleFunc("GET /blueprints", d.handleListBlueprints)
	d.mux.HandleFunc("GET /blueprints/{name}", d.handleGetBlueprint)
	d.mux.HandleFunc("GET /executions", d.handleListExecutions)
	d.mux.HandleFunc("GET /executions/{id}", d.handleGetExecution)
}

// handleCreateObjective decodes a JSON body with a description, creates
// a new objective, publishes an event, and responds with the created objective.
func (d *Daemon) handleCreateObjective(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	obj := &domain.Objective{
		Description: req.Description,
	}
	if err := d.objectives.Create(r.Context(), obj); err != nil {
		d.logger.Error("creating objective", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create objective")
		return
	}

	d.eventBus.Publish(domain.Event{
		Type:      domain.EventObjectiveCreated,
		Objective: obj.ID,
		Payload:   obj.Description,
		CreatedAt: time.Now(),
	})

	writeJSON(w, http.StatusCreated, obj)
}

// handleGetObjective retrieves a single objective by ID from the path.
func (d *Daemon) handleGetObjective(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	obj, err := d.objectives.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "objective not found")
			return
		}
		d.logger.Error("getting objective", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get objective")
		return
	}

	writeJSON(w, http.StatusOK, obj)
}

// handleListObjectives returns all objectives as a JSON array.
func (d *Daemon) handleListObjectives(w http.ResponseWriter, r *http.Request) {
	objectives, err := d.objectives.List(r.Context())
	if err != nil {
		d.logger.Error("listing objectives", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list objectives")
		return
	}

	if objectives == nil {
		objectives = []domain.Objective{}
	}

	writeJSON(w, http.StatusOK, objectives)
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

// handleListExecutions returns all blueprint executions as a JSON array.
func (d *Daemon) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	executions, err := d.executions.List(r.Context())
	if err != nil {
		d.logger.Error("listing executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list executions")
		return
	}

	if executions == nil {
		executions = []blueprint.Execution{}
	}

	writeJSON(w, http.StatusOK, executions)
}

// handleGetExecution returns a specific execution by ID.
func (d *Daemon) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	exec, err := d.executions.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		d.logger.Error("getting execution", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return
	}

	writeJSON(w, http.StatusOK, exec)
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
