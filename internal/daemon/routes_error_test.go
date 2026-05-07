package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/harness/preflight"
)

func TestWritePreflightErrorPreservesValidationFindings(t *testing.T) {
	failure := preflight.Failure{BlueprintID: "standard", Problems: []preflight.Problem{{
		Requirement: "runtime_auth",
		Summary:     "agent steps require usable openai credentials",
		Evidence:    "credential not found",
		Fix:         "tack auth add openai",
	}}}
	err := fmt.Errorf("approving execution: %w", failure)

	rec := httptest.NewRecorder()
	if !writePreflightError(rec, err) {
		t.Fatal("writePreflightError returned false")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "preflight failed for blueprint") {
		t.Fatalf("error = %q", resp.Error)
	}
	if len(resp.Findings) != 1 || resp.Findings[0].Summary != "agent steps require usable openai credentials" {
		t.Fatalf("findings = %#v", resp.Findings)
	}
	if resp.Findings[0].Details["blueprint_id"] != "standard" || resp.Findings[0].Fix != "tack auth add openai" {
		t.Fatalf("finding = %#v", resp.Findings[0])
	}
}
