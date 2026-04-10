package daemonauth

import "testing"

func TestIssueAndParseAgentToken(t *testing.T) {
	claims := AgentClaims{
		ProjectID:   "proj-1",
		ObjectiveID: "obj-1",
		StreamID:    "stream-1",
		AgentName:   "builder-1",
		Role:        "builder",
	}
	tok, err := IssueAgentToken("secret", claims)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}
	got, err := ParseAgentToken(tok, "secret")
	if err != nil {
		t.Fatalf("ParseAgentToken: %v", err)
	}
	if *got != claims {
		t.Fatalf("claims = %+v, want %+v", *got, claims)
	}
	if _, err := ParseAgentToken(tok, "wrong-secret"); err == nil {
		t.Fatal("expected signature validation failure")
	}
}
