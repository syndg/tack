package daemon

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/preflight"
	"github.com/syndg/tack/internal/runtimecatalog"
	"github.com/syndg/tack/internal/validation"
)

// registerRoutes sets up all HTTP route handlers on the daemon's mux.
func (d *Daemon) registerRoutes() {
	// Projects
	d.mux.HandleFunc("POST /projects/register", d.handleRegisterProject)
	d.mux.HandleFunc("GET /projects", d.handleListProjects)
	d.mux.HandleFunc("GET /projects/resolve", d.handleResolveProject)
	d.mux.HandleFunc("GET /projects/{id}", d.handleGetProject)
	d.mux.HandleFunc("POST /projects/{id}/relink", d.handleRelinkProject)
	d.mux.HandleFunc("DELETE /projects/{id}", d.handleRemoveProject)

	// Objectives
	d.mux.HandleFunc("POST /objectives", d.handleCreateObjective)
	d.mux.HandleFunc("GET /objectives/{id}", d.handleGetObjective)
	d.mux.HandleFunc("GET /objectives", d.handleListObjectives)
	d.mux.HandleFunc("GET /objectives/{id}/dossier", d.handleGetObjectiveDossier)
	d.mux.HandleFunc("GET /objectives/{id}/insights", d.handleGetObjectiveInsightReport)
	d.mux.HandleFunc("GET /insights/{id}", d.handleGetInsightDetail)
	d.mux.HandleFunc("POST /insights/{id}/promote", d.handlePromoteInsightSource)
	d.mux.HandleFunc("POST /insights/{id}/reject", d.handleRejectInsightSource)
	d.mux.HandleFunc("GET /objectives/{id}/codification-candidates", d.handleListObjectiveCodificationCandidates)
	d.mux.HandleFunc("PUT /objectives/{id}/dossier", d.handleUpdateObjectiveDossier)
	d.mux.HandleFunc("GET /objectives/{id}/plan", d.handleGetObjectivePlan)
	d.mux.HandleFunc("POST /objectives/{id}/execute", d.handleExecuteObjective)

	// Plans & Streams
	d.mux.HandleFunc("POST /plans", d.handleCreatePlan)
	d.mux.HandleFunc("GET /plans", d.handleListPlans)
	d.mux.HandleFunc("GET /plans/{id}", d.handleGetPlan)
	d.mux.HandleFunc("POST /plans/{id}/quality-gates", d.handleUpdatePlanQualityGates)
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

	// Runs — the run-centric orchestration boundary.
	// POST /runs/{id}/command is the primary intervention endpoint. The
	// execution-level approve/retry and agent kill routes above are
	// convenience wrappers that resolve the run and delegate to Command.
	d.mux.HandleFunc("GET /runs/{id}/snapshot", d.handleGetRunSnapshot)
	d.mux.HandleFunc("POST /runs/{id}/command", d.handleRunCommand)
	d.mux.HandleFunc("GET /objectives/{id}/run", d.handleGetObjectiveRunSnapshot)

	// Blueprints & System
	d.mux.HandleFunc("GET /blueprints", d.handleListBlueprints)
	d.mux.HandleFunc("GET /blueprints/{id}", d.handleGetBlueprint)
	d.mux.HandleFunc("GET /events", d.handleSSE)
	d.mux.HandleFunc("GET /health", d.handleHealth)
	d.mux.HandleFunc("GET /status", d.handleStatus)
	d.mux.HandleFunc("POST /reload", d.handleReload)
	d.mux.HandleFunc("GET /runtime/pi/visibility", d.handlePiVisibility)
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

func (d *Daemon) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := d.Reload(r.Context()); err != nil {
		d.logger.Warn("live daemon reload failed", "error", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (d *Daemon) handlePiVisibility(w http.ResponseWriter, r *http.Request) {
	probe := runtimecatalog.ProbePi(r.Context(), runtimecatalog.ExecRunner{})
	writeJSON(w, http.StatusOK, probe)
}

// handleListBlueprints returns all available blueprints as a JSON array.
func (d *Daemon) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	projectCtx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}
	ids := projectCtx.BlueprintRegistry.List()
	blueprints := make([]blueprint.Blueprint, 0, len(ids))
	for _, id := range ids {
		bp, ok := projectCtx.BlueprintRegistry.Get(id)
		if ok {
			blueprints = append(blueprints, *bp)
		}
	}
	writeJSON(w, http.StatusOK, blueprints)
}

// handleGetBlueprint returns a specific blueprint by ID.
func (d *Daemon) handleGetBlueprint(w http.ResponseWriter, r *http.Request) {
	projectCtx, ok := d.requireProjectContext(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	bp, ok := projectCtx.BlueprintRegistry.Get(id)
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
	if err := json.NewEncoder(w).Encode(errorResponse{Error: message}); err != nil {
		slog.Error("encoding JSON error response", "status", status, "error", err)
	}
}

type errorResponse struct {
	Error    string               `json:"error"`
	Findings []validation.Finding `json:"findings,omitempty"`
}

func writeValidationError(w http.ResponseWriter, status int, message string, findings []validation.Finding) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errorResponse{Error: message, Findings: findings}); err != nil {
		slog.Error("encoding JSON validation error response", "status", status, "error", err)
	}
}

func writePreflightError(w http.ResponseWriter, err error) bool {
	var failure preflight.Failure
	if !errors.As(err, &failure) {
		return false
	}
	writeValidationError(w, http.StatusUnprocessableEntity, failure.Error(), failure.Findings())
	return true
}
