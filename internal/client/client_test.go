package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
