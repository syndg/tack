package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/syndg/deck/internal/domain"
)

// Client communicates with the Deck daemon over HTTP.
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

// CreateObjective sends a POST /objectives request to create a new objective.
func (c *Client) CreateObjective(ctx context.Context, description string) (*domain.Objective, error) {
	body := struct {
		Description string `json:"description"`
	}{Description: description}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/objectives", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating objective: %w", err)
	}
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	resp.Body.Close()
	return nil
}

// RejectPlan rejects a plan.
func (c *Client) RejectPlan(ctx context.Context, planID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/plans/"+planID+"/reject", nil)
	if err != nil {
		return fmt.Errorf("rejecting plan: %w", err)
	}
	resp.Body.Close()
	return nil
}

// GetStatus sends a GET /status request.
func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, fmt.Errorf("getting status: %w", err)
	}
	defer resp.Body.Close()

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding status response: %w", err)
	}
	return &status, nil
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
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}
