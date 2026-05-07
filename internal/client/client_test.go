package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/insightreport"
	"github.com/syndg/tack/internal/validation"
)

func TestDoReturnsErrorForNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.GetStatus(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP status in error, got %v", err)
	}
}

func TestDoRendersStructuredValidationError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			ErrorText: "preflight failed for blueprint \"standard\"",
			Findings: []validation.Finding{{
				Check:    "preflight.git_push_permission",
				Status:   validation.StatusFail,
				Summary:  "create_pr requires push permission to owner/repo",
				Evidence: "permissions.push=false",
				Fix:      "use a GitHub token with push permission for this repository, or remove create_pr from the selected blueprint",
			}},
		})
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.GetStatus(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	for _, want := range []string{"HTTP 422", "preflight failed", "create_pr requires push permission", "permissions.push=false", "remove create_pr"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error missing %q:\n%s", want, text)
		}
	}
}

func TestGetObjectiveInsightReportWrapsEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/objectives/obj-1/insights" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Tack-Project-ID"); got != "project-1" {
			t.Fatalf("project header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(insightreport.Report{ObjectiveID: "obj-1", Summary: insightreport.Summary{TotalInsights: 1}})
	}))
	defer ts.Close()

	c := New(ts.URL)
	c.SetProjectID("project-1")
	report, err := c.GetObjectiveInsightReport(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("GetObjectiveInsightReport: %v", err)
	}
	if report.ObjectiveID != "obj-1" || report.Summary.TotalInsights != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestGetInsightDetailWrapsEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/insights/insight-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Tack-Project-ID"); got != "project-1" {
			t.Fatalf("project header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(insightreport.Detail{Kind: insightreport.DetailKindInsight, Insight: &domain.ObjectiveInsight{ID: "insight-1"}})
	}))
	defer ts.Close()

	c := New(ts.URL)
	c.SetProjectID("project-1")
	detail, err := c.GetInsightDetail(context.Background(), "insight-1")
	if err != nil {
		t.Fatalf("GetInsightDetail: %v", err)
	}
	if detail.Kind != insightreport.DetailKindInsight || detail.Insight == nil || detail.Insight.ID != "insight-1" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestPromoteAndRejectInsightSourceWrapEndpoints(t *testing.T) {
	requests := []string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("X-Tack-Project-ID"); got != "project-1" {
			t.Fatalf("project header = %q", got)
		}
		switch r.URL.Path {
		case "/insights/candidate-1/promote":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"target":"project-memory"`) {
				t.Fatalf("promote body = %s", body)
			}
			_ = json.NewEncoder(w).Encode(domain.PromotionRecord{ID: "promotion-1", SourceCandidateID: "candidate-1", Target: domain.PromotionTargetProjectMemory, Status: domain.PromotionStatusApproved})
		case "/insights/candidate-1/reject":
			_ = json.NewEncoder(w).Encode(domain.PromotionRecord{ID: "promotion-2", SourceCandidateID: "candidate-1", Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusRejected})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c := New(ts.URL)
	c.SetProjectID("project-1")
	promoted, err := c.PromoteInsightSource(context.Background(), "candidate-1", domain.PromotionTargetProjectMemory)
	if err != nil {
		t.Fatalf("PromoteInsightSource: %v", err)
	}
	rejected, err := c.RejectInsightSource(context.Background(), "candidate-1")
	if err != nil {
		t.Fatalf("RejectInsightSource: %v", err)
	}
	if promoted.Status != domain.PromotionStatusApproved || rejected.Status != domain.PromotionStatusRejected || len(requests) != 2 {
		t.Fatalf("promoted=%+v rejected=%+v requests=%v", promoted, rejected, requests)
	}
}
