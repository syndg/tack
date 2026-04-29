package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/syndg/tack/internal/daemonauth"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/insightreport"
	"github.com/syndg/tack/internal/services/merge"
)

// Client communicates with the Tack daemon over HTTP.
type Client struct {
	baseURL    string
	projectID  string
	authToken  string
	httpClient *http.Client
}

type ProjectRegistration struct {
	ProjectID  string `json:"project_id,omitempty"`
	Name       string `json:"name,omitempty"`
	RootPath   string `json:"root_path"`
	ConfigPath string `json:"config_path,omitempty"`
}

// StatusResponse is the response from the /status endpoint.
type StatusResponse struct {
	Status     string         `json:"status"`
	Uptime     string         `json:"uptime"`
	Objectives map[string]int `json:"objectives"`
}

// New creates a new Client targeting the given daemon base URL.
func New(baseURL string) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	if token, err := daemonauth.Load(); err == nil && token != "" {
		c.authToken = token
	}
	return c
}

// SetProjectID configures the default project target header.
func (c *Client) SetProjectID(projectID string) {
	c.projectID = projectID
}

// SetAuthToken configures bearer auth for daemon requests.
func (c *Client) SetAuthToken(token string) {
	c.authToken = token
}

// ResolveProjectByPath resolves a registered project from a filesystem path.
func (c *Client) ResolveProjectByPath(ctx context.Context, path string) (*domain.Project, error) {
	resp, err := c.do(ctx, http.MethodGet, "/projects/resolve?path="+url.QueryEscape(path), nil)
	if err != nil {
		return nil, fmt.Errorf("resolving project: %w", err)
	}
	defer closeBody(resp)
	var project domain.Project
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return nil, fmt.Errorf("decoding project response: %w", err)
	}
	return &project, nil
}

// RegisterProject adds or refreshes a project registration.
func (c *Client) RegisterProject(ctx context.Context, req ProjectRegistration) (*domain.Project, error) {
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling project registration: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/projects/register", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("registering project: %w", err)
	}
	defer closeBody(resp)
	var project domain.Project
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return nil, fmt.Errorf("decoding project response: %w", err)
	}
	return &project, nil
}

// ListProjects returns all registered projects.
func (c *Client) ListProjects(ctx context.Context) ([]domain.Project, error) {
	resp, err := c.do(ctx, http.MethodGet, "/projects", nil)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer closeBody(resp)
	var projects []domain.Project
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, fmt.Errorf("decoding projects response: %w", err)
	}
	return projects, nil
}

// GetProject returns a single registered project.
func (c *Client) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	resp, err := c.do(ctx, http.MethodGet, "/projects/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}
	defer closeBody(resp)
	var project domain.Project
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return nil, fmt.Errorf("decoding project response: %w", err)
	}
	return &project, nil
}

// RelinkProject moves a project registration to a new root path.
func (c *Client) RelinkProject(ctx context.Context, id, rootPath, configPath string) (*domain.Project, error) {
	body, err := json.Marshal(ProjectRegistration{RootPath: rootPath, ConfigPath: configPath})
	if err != nil {
		return nil, fmt.Errorf("marshaling relink request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/projects/"+id+"/relink", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("relinking project: %w", err)
	}
	defer closeBody(resp)
	var project domain.Project
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		return nil, fmt.Errorf("decoding project response: %w", err)
	}
	return &project, nil
}

// RemoveProject removes a registered project.
func (c *Client) RemoveProject(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/projects/"+id, nil)
	if err != nil {
		return fmt.Errorf("removing project: %w", err)
	}
	closeBody(resp)
	return nil
}

// CreateObjectiveOptions configures objective creation.
type CreateObjectiveOptions struct {
	Blueprint string `json:"blueprint,omitempty"`
}

func (c *Client) GetObjectiveDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives/"+objectiveID+"/dossier", nil)
	if err != nil {
		return nil, fmt.Errorf("getting objective dossier: %w", err)
	}
	defer closeBody(resp)
	var dossier domain.Dossier
	if err := json.NewDecoder(resp.Body).Decode(&dossier); err != nil {
		return nil, fmt.Errorf("decoding dossier response: %w", err)
	}
	return &dossier, nil
}

func (c *Client) UpdateObjectiveDossier(ctx context.Context, objectiveID string, dossier domain.Dossier) (*domain.Dossier, error) {
	jsonBody, err := json.Marshal(dossier)
	if err != nil {
		return nil, fmt.Errorf("marshaling dossier body: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPut, "/objectives/"+objectiveID+"/dossier", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("updating objective dossier: %w", err)
	}
	defer closeBody(resp)
	var updated domain.Dossier
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		return nil, fmt.Errorf("decoding updated dossier response: %w", err)
	}
	return &updated, nil
}

// CreateObjective sends a POST /objectives request to create a new objective.
func (c *Client) CreateObjective(ctx context.Context, description string) (*domain.Objective, error) {
	return c.CreateObjectiveWithOptions(ctx, description, CreateObjectiveOptions{})
}

// CreateObjectiveWithOptions creates an objective with optional blueprint selection.
func (c *Client) CreateObjectiveWithOptions(ctx context.Context, description string, opts CreateObjectiveOptions) (*domain.Objective, error) {
	body := struct {
		Description string `json:"description"`
		Blueprint   string `json:"blueprint,omitempty"`
	}{
		Description: description,
		Blueprint:   opts.Blueprint,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/objectives", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating objective: %w", err)
	}
	defer closeBody(resp)

	var obj domain.Objective
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("decoding objective response: %w", err)
	}
	return &obj, nil
}

// GetObjective sends a GET /objectives/{id} request.
func (c *Client) GetObjective(ctx context.Context, id string) (*domain.Objective, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting objective: %w", err)
	}
	defer closeBody(resp)

	var obj domain.Objective
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, fmt.Errorf("decoding objective response: %w", err)
	}
	return &obj, nil
}

// ListObjectives sends a GET /objectives request.
func (c *Client) ListObjectives(ctx context.Context) ([]domain.Objective, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives", nil)
	if err != nil {
		return nil, fmt.Errorf("listing objectives: %w", err)
	}
	defer closeBody(resp)

	var objectives []domain.Objective
	if err := json.NewDecoder(resp.Body).Decode(&objectives); err != nil {
		return nil, fmt.Errorf("decoding objectives response: %w", err)
	}
	return objectives, nil
}

// PlanResponse represents a plan with its streams.
type PlanResponse struct {
	Plan    domain.Plan     `json:"plan"`
	Streams []domain.Stream `json:"streams"`
}

// ListPlans returns all plans.
func (c *Client) ListPlans(ctx context.Context) ([]domain.Plan, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plans", nil)
	if err != nil {
		return nil, fmt.Errorf("listing plans: %w", err)
	}
	defer closeBody(resp)

	var plans []domain.Plan
	if err := json.NewDecoder(resp.Body).Decode(&plans); err != nil {
		return nil, fmt.Errorf("decoding plans response: %w", err)
	}
	return plans, nil
}

// GetPlan returns a plan with its streams.
func (c *Client) GetPlan(ctx context.Context, id string) (*PlanResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/plans/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting plan: %w", err)
	}
	defer closeBody(resp)

	var pr PlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding plan response: %w", err)
	}
	return &pr, nil
}

// GetObjectivePlan returns the plan for an objective.
func (c *Client) GetObjectivePlan(ctx context.Context, objectiveID string) (*PlanResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives/"+objectiveID+"/plan", nil)
	if err != nil {
		return nil, fmt.Errorf("getting objective plan: %w", err)
	}
	defer closeBody(resp)

	var pr PlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding plan response: %w", err)
	}
	return &pr, nil
}

func (c *Client) GetObjectiveInsightReport(ctx context.Context, objectiveID string) (*insightreport.Report, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives/"+objectiveID+"/insights", nil)
	if err != nil {
		return nil, fmt.Errorf("getting objective insight report: %w", err)
	}
	defer closeBody(resp)

	var report insightreport.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, fmt.Errorf("decoding insight report response: %w", err)
	}
	return &report, nil
}

func (c *Client) PromoteInsightSource(ctx context.Context, sourceID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	body := struct {
		Target domain.PromotionTarget `json:"target"`
	}{Target: target}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling promotion request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/insights/"+sourceID+"/promote", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("promoting insight source: %w", err)
	}
	defer closeBody(resp)
	var record domain.PromotionRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("decoding promotion response: %w", err)
	}
	return &record, nil
}

func (c *Client) RejectInsightSource(ctx context.Context, sourceID string) (*domain.PromotionRecord, error) {
	resp, err := c.do(ctx, http.MethodPost, "/insights/"+sourceID+"/reject", nil)
	if err != nil {
		return nil, fmt.Errorf("rejecting insight source: %w", err)
	}
	defer closeBody(resp)
	var record domain.PromotionRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("decoding promotion response: %w", err)
	}
	return &record, nil
}

// UpdatePlanQualityGates replaces a plan's quality gates.
func (c *Client) UpdatePlanQualityGates(ctx context.Context, planID string, qualityGates []string) (*domain.Plan, error) {
	body := struct {
		QualityGates []string `json:"quality_gates"`
	}{QualityGates: qualityGates}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling quality gates request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/plans/"+planID+"/quality-gates", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("updating plan quality gates: %w", err)
	}
	defer closeBody(resp)
	var plan domain.Plan
	if err := json.NewDecoder(resp.Body).Decode(&plan); err != nil {
		return nil, fmt.Errorf("decoding updated plan response: %w", err)
	}
	return &plan, nil
}

// ApprovePlan approves a plan for execution.
func (c *Client) ApprovePlan(ctx context.Context, planID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/plans/"+planID+"/approve", nil)
	if err != nil {
		return fmt.Errorf("approving plan: %w", err)
	}
	closeBody(resp)
	return nil
}

// RejectPlan rejects a plan.
func (c *Client) RejectPlan(ctx context.Context, planID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/plans/"+planID+"/reject", nil)
	if err != nil {
		return fmt.Errorf("rejecting plan: %w", err)
	}
	closeBody(resp)
	return nil
}

// ExecuteObjective triggers execution for an approved objective.
func (c *Client) ExecuteObjective(ctx context.Context, objectiveID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/objectives/"+objectiveID+"/execute", nil)
	if err != nil {
		return fmt.Errorf("executing objective: %w", err)
	}
	closeBody(resp)
	return nil
}

// ListAgents returns all agent sessions.
func (c *Client) ListAgents(ctx context.Context) ([]domain.AgentSession, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	defer closeBody(resp)

	var agents []domain.AgentSession
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, fmt.Errorf("decoding agents response: %w", err)
	}
	return agents, nil
}

// GetAgent returns an agent session by ID.
func (c *Client) GetAgent(ctx context.Context, id string) (*domain.AgentSession, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agents/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting agent: %w", err)
	}
	defer closeBody(resp)

	var agent domain.AgentSession
	if err := json.NewDecoder(resp.Body).Decode(&agent); err != nil {
		return nil, fmt.Errorf("decoding agent response: %w", err)
	}
	return &agent, nil
}

// RunSnapshot returns the snapshot for a run by ID.
func (c *Client) RunSnapshot(ctx context.Context, runID string) (*domain.Snapshot, error) {
	resp, err := c.do(ctx, http.MethodGet, "/runs/"+runID+"/snapshot", nil)
	if err != nil {
		return nil, fmt.Errorf("getting run snapshot: %w", err)
	}
	defer closeBody(resp)

	var snap domain.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decoding snapshot response: %w", err)
	}
	return &snap, nil
}

// ObjectiveRunSnapshot returns the snapshot for the most recent run of an objective.
func (c *Client) ObjectiveRunSnapshot(ctx context.Context, objectiveID string) (*domain.Snapshot, error) {
	resp, err := c.do(ctx, http.MethodGet, "/objectives/"+objectiveID+"/run", nil)
	if err != nil {
		return nil, fmt.Errorf("getting objective run snapshot: %w", err)
	}
	defer closeBody(resp)

	var snap domain.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decoding snapshot response: %w", err)
	}
	return &snap, nil
}

// RunCommand sends an intervention command to a run.
func (c *Client) RunCommand(ctx context.Context, runID string, cmd domain.Command) (*domain.Snapshot, error) {
	jsonBody, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshaling command: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/runs/"+runID+"/command", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("sending run command: %w", err)
	}
	defer closeBody(resp)

	var snap domain.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decoding command response: %w", err)
	}
	return &snap, nil
}

// KillAgent terminates an active agent.
func (c *Client) KillAgent(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agents/"+id+"/kill", nil)
	if err != nil {
		return fmt.Errorf("killing agent: %w", err)
	}
	closeBody(resp)
	return nil
}

// ListMail returns unread messages for an agent.
func (c *Client) ListMail(ctx context.Context, agentName string) ([]domain.MailMessage, error) {
	resp, err := c.do(ctx, http.MethodGet, "/mail/"+agentName+"/unread", nil)
	if err != nil {
		return nil, fmt.Errorf("listing mail: %w", err)
	}
	defer closeBody(resp)

	var messages []domain.MailMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, fmt.Errorf("decoding mail response: %w", err)
	}
	return messages, nil
}

// SendMail sends a message to an agent or broadcast group.
func (c *Client) SendMail(ctx context.Context, msg *domain.MailMessage) error {
	jsonBody, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling mail message: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/mail", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("sending mail: %w", err)
	}
	closeBody(resp)
	return nil
}

// GetStatus sends a GET /status request.
func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, fmt.Errorf("getting status: %w", err)
	}
	defer closeBody(resp)

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding status response: %w", err)
	}
	return &status, nil
}

// ListMergeQueue returns merge queue entries, optionally filtered by objective.
func (c *Client) ListMergeQueue(ctx context.Context, objectiveID string) ([]domain.MergeEntry, error) {
	path := "/merge-queue"
	if objectiveID != "" {
		path += "?objective=" + objectiveID
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("listing merge queue: %w", err)
	}
	defer closeBody(resp)

	var entries []domain.MergeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decoding merge queue response: %w", err)
	}
	return entries, nil
}

// GetMergeEntry returns a single merge queue entry.
func (c *Client) GetMergeEntry(ctx context.Context, id string) (*domain.MergeEntry, error) {
	resp, err := c.do(ctx, http.MethodGet, "/merge-queue/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting merge entry: %w", err)
	}
	defer closeBody(resp)

	var entry domain.MergeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("decoding merge entry response: %w", err)
	}
	return &entry, nil
}

// RetryMerge resets a failed merge entry to pending.
func (c *Client) RetryMerge(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/merge-queue/"+id+"/retry", nil)
	if err != nil {
		return fmt.Errorf("retrying merge: %w", err)
	}
	closeBody(resp)
	return nil
}

// GetStreamDiff returns the diff summary for a merged stream.
func (c *Client) GetStreamDiff(ctx context.Context, streamID string) (*merge.DiffSummary, error) {
	resp, err := c.do(ctx, http.MethodGet, "/streams/"+streamID+"/diff", nil)
	if err != nil {
		return nil, fmt.Errorf("getting stream diff: %w", err)
	}
	defer closeBody(resp)

	var diff merge.DiffSummary
	if err := json.NewDecoder(resp.Body).Decode(&diff); err != nil {
		return nil, fmt.Errorf("decoding stream diff response: %w", err)
	}
	return &diff, nil
}

// ListExecutions returns all blueprint executions.
func (c *Client) ListExecutions(ctx context.Context) ([]blueprint.Execution, error) {
	resp, err := c.do(ctx, http.MethodGet, "/executions", nil)
	if err != nil {
		return nil, fmt.Errorf("listing executions: %w", err)
	}
	defer closeBody(resp)

	var executions []blueprint.Execution
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		return nil, fmt.Errorf("decoding executions response: %w", err)
	}
	return executions, nil
}

// GetExecution returns a single blueprint execution by ID.
func (c *Client) GetExecution(ctx context.Context, id string) (*blueprint.Execution, error) {
	resp, err := c.do(ctx, http.MethodGet, "/executions/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting execution: %w", err)
	}
	defer closeBody(resp)

	var execution blueprint.Execution
	if err := json.NewDecoder(resp.Body).Decode(&execution); err != nil {
		return nil, fmt.Errorf("decoding execution response: %w", err)
	}
	return &execution, nil
}

// ListBlueprints returns all available blueprints.
func (c *Client) ListBlueprints(ctx context.Context) ([]blueprint.Blueprint, error) {
	resp, err := c.do(ctx, http.MethodGet, "/blueprints", nil)
	if err != nil {
		return nil, fmt.Errorf("listing blueprints: %w", err)
	}
	defer closeBody(resp)

	var blueprints []blueprint.Blueprint
	if err := json.NewDecoder(resp.Body).Decode(&blueprints); err != nil {
		return nil, fmt.Errorf("decoding blueprints response: %w", err)
	}
	return blueprints, nil
}

// GetBlueprint returns a single blueprint by ID.
func (c *Client) GetBlueprint(ctx context.Context, id string) (*blueprint.Blueprint, error) {
	resp, err := c.do(ctx, http.MethodGet, "/blueprints/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("getting blueprint: %w", err)
	}
	defer closeBody(resp)

	var bp blueprint.Blueprint
	if err := json.NewDecoder(resp.Body).Decode(&bp); err != nil {
		return nil, fmt.Errorf("decoding blueprint response: %w", err)
	}
	return &bp, nil
}

// do executes an HTTP request and returns the response. It returns an error
// for non-2xx status codes, including the status code and response body.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.projectID != "" {
		req.Header.Set("X-Tack-Project-ID", c.projectID)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close() //nolint:errcheck // error path, body is read-only
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}

// closeBody closes an HTTP response body, intentionally ignoring close errors.
func closeBody(resp *http.Response) {
	_ = resp.Body.Close()
}
