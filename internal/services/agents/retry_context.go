package agents

import "github.com/syndg/tack/internal/domain"

// RetryContext is the normalized recovery context passed into a rerun attempt.
type RetryContext struct {
	AttemptNumber int
	MaxAttempts   int
	FailureKind   domain.FailureKind
	LastError     string
	HumanGuidance string
}
