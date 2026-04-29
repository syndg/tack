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
