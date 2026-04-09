package agents

import "strings"

const (
	reviewDecisionPrefix = "REVIEW_DECISION:"
	reviewFeedbackPrefix = "REVIEW_FEEDBACK:"
)

type ReviewOutcome struct {
	Approved bool
	Feedback string
}

func ParseReviewOutcome(summary string) (ReviewOutcome, bool) {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ReviewOutcome{}, false
	}

	lines := strings.Split(trimmed, "\n")
	decision := ""
	feedback := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, reviewDecisionPrefix):
			decision = strings.ToLower(strings.TrimSpace(line[len(reviewDecisionPrefix):]))
		case strings.HasPrefix(upper, reviewFeedbackPrefix):
			feedback = strings.TrimSpace(line[len(reviewFeedbackPrefix):])
			if feedback == "" && i+1 < len(lines) {
				feedback = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			}
		}
	}

	switch decision {
	case "approve", "approved", "pass":
		return ReviewOutcome{Approved: true}, true
	case "reject", "rejected", "changes_requested", "changes requested":
		if feedback == "" {
			feedback = trimmed
		}
		return ReviewOutcome{Approved: false, Feedback: feedback}, true
	}

	heuristic := strings.ToLower(trimmed)
	if strings.Contains(heuristic, "changes requested") || strings.Contains(heuristic, "needs changes") || strings.Contains(heuristic, "request changes") || strings.Contains(heuristic, "review_decision: reject") {
		return ReviewOutcome{Approved: false, Feedback: trimmed}, true
	}

	return ReviewOutcome{}, false
}
