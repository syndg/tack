package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/services/merge"
)

// Client communicates with the Tack daemon over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// StatusResponse is the response from the /status endpoint.
type StatusResponse struct {
	Status     string         `json:"status"`
	Uptime     string         `json:"uptime"`
	Objectives map[string]int `json:"objectives"`
}

// New creates a new Client targeting the given daemon base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateObjectiveOptions configures objective creation.
type CreateObjectiveOptions struct {
	Blueprint string `json:"blueprint,omitempty"`
}

// CreateObjective sends a POST /objectives request to create a new objective.
func (c *Client) CreateObjective(ctx context.Context, description string) (*domain.Objective, error) {
	return c.CreateObjectiveWithOptions(ctx, description, CreateObjectiveOptions{})
}

// CreateObjectiveWithOptions creates an objective with optional blueprint setting.
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

// CreateObjectiveSimpleResponse holds the response when creating an objective in simple mode.
// The server auto-creates and approves a single-stream plan alongside the objective.
type CreateObjectiveSimpleResponse struct {
	Objective domain.Objective `json:"objective"`
	Plan      domain.Plan      `json:"plan"`
}

// CreateObjectiveSimple creates an objective in simple mode (single-agent, no decomposition).
// It POSTs /objectives with simple=true and an optional blueprint override.
// The server responds with both the objective and the auto-approved plan.
func (c *Client) CreateObjectiveSimple(ctx context.Context, description, blueprint string) (*CreateObjectiveSimpleResponse, error) {
	body := struct {
		Description string `json:"description"`
		Blueprint   string `json:"blueprint,omitempty"`
		Simple      bool   `json:"simple"`
	}{
		Description: description,
		Blueprint:   blueprint,
		Simple:      true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/objectives", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating simple objective: %w", err)
	}
	defer closeBody(resp)

	var result CreateObjectiveSimpleResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding simple objective response: %w", err)
	}
	return &result, nil
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

// KillAgent terminates an active agent.
func (c *Client) KillAgent(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agents/"+id+"/kill", nil)
	if err != nil {
		return fmt.Errorf("killing agent: %w", err)
	}
	closeBody(resp)
	return nil
}

// ApproveExecution approves a human gate in a blueprint execution.
func (c *Client) ApproveExecution(ctx context.Context, executionID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/executions/"+executionID+"/approve", nil)
	if err != nil {
		return fmt.Errorf("approving execution: %w", err)
	}
	closeBody(resp)
	return nil
}

// RetryExecution retries a failed stream sub-execution with optional human guidance.
func (c *Client) RetryExecution(ctx context.Context, executionID string, guidance string) error {
	body := struct {
		Guidance string `json:"guidance,omitempty"`
	}{Guidance: guidance}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling retry request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/executions/"+executionID+"/retry", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("retrying execution: %w", err)
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

// GetBlueprint returns a single blueprint by name.
func (c *Client) GetBlueprint(ctx context.Context, name string) (*blueprint.Blueprint, error) {
	resp, err := c.do(ctx, http.MethodGet, "/blueprints/"+name, nil)
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
