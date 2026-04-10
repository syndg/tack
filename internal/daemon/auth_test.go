package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/syndg/tack/internal/daemonauth"
)

func TestAuthMiddleware_AllowsScopedAgentMail(t *testing.T) {
	d := &Daemon{authToken: "secret"}
	tok, err := daemonauth.IssueAgentToken("secret", daemonauth.AgentClaims{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		AgentName:   "builder-1",
	})
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}
	h := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := agentClaimsFromRequest(r)
		if claims == nil || claims.AgentName != "builder-1" {
			t.Fatalf("missing agent claims: %+v", claims)
		}
		writeJSON(w, http.StatusOK, map[string]string{"project_id": d.targetProjectID(r)})
	}))
	req := httptest.NewRequest(http.MethodPost, "/mail", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["project_id"] != "proj-1" {
		t.Fatalf("project_id = %q, want proj-1", body["project_id"])
	}
}

func TestAuthMiddleware_BlocksAgentOnOperatorRoutes(t *testing.T) {
	d := &Daemon{authToken: "secret"}
	tok, err := daemonauth.IssueAgentToken("secret", daemonauth.AgentClaims{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		AgentName:   "builder-1",
	})
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}
	h := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/runs/run-1/command", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	daemonReq := httptest.NewRequest(http.MethodPost, "/runs/run-1/command", nil)
	daemonReq.Header.Set("Authorization", "Bearer secret")
	daemonRec := httptest.NewRecorder()
	h.ServeHTTP(daemonRec, daemonReq)
	if daemonRec.Code != http.StatusOK {
		t.Fatalf("daemon status = %d, want 200", daemonRec.Code)
	}
}
