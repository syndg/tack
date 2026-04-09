package agents

import "testing"

func TestParseReviewOutcomeReject(t *testing.T) {
	outcome, ok := ParseReviewOutcome("Looks close\nREVIEW_DECISION: reject\nREVIEW_FEEDBACK:\nAdd coverage for the error path")
	if !ok {
		t.Fatal("expected structured review outcome")
	}
	if outcome.Approved {
		t.Fatal("expected rejection")
	}
	if outcome.Feedback != "Add coverage for the error path" {
		t.Fatalf("feedback = %q", outcome.Feedback)
	}
}

func TestParseReviewOutcomeApprove(t *testing.T) {
	outcome, ok := ParseReviewOutcome("REVIEW_DECISION: approve")
	if !ok {
		t.Fatal("expected structured review outcome")
	}
	if !outcome.Approved {
		t.Fatal("expected approval")
	}
}
