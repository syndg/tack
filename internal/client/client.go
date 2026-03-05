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
