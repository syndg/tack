package dispatch

import (
	"context"
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
)

func (c *Coordinator) recoveryBlockAttempt(ctx context.Context, streamID string) (*domain.Attempt, error) {
	latest, err := c.latestRecoveryAttempt(ctx, streamID)
	if err != nil || latest == nil {
		return nil, err
	}
	if latest.Action != domain.RecoveryActionAskHumanThenResume {
		return nil, nil
	}
	switch latest.Status {
	case domain.AttemptStatusBlocked, domain.AttemptStatusExhausted:
		return latest, nil
	default:
		return nil, nil
	}
}

func (c *Coordinator) latestRecoveryAttempt(ctx context.Context, streamID string) (*domain.Attempt, error) {
	if c.attempts == nil || streamID == "" {
		return nil, nil
	}
	attempts, err := c.attempts.ListByStream(ctx, streamID)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 {
		return nil, nil
	}
	latest := attempts[len(attempts)-1]
	return &latest, nil
}

func buildRetryFixContext(lastError string, blocked *domain.Attempt, guidance string) string {
	var parts []string
	if blocked != nil && blocked.FixContext != "" {
		parts = append(parts, strings.TrimSpace(blocked.FixContext))
	}
	if blocked != nil && blocked.ErrorSummary != "" && blocked.ErrorSummary != lastError {
		parts = append(parts, fmt.Sprintf("Recovery blocker:\n%s", blocked.ErrorSummary))
	}
	if lastError != "" {
		parts = append(parts, fmt.Sprintf("Previous error:\n%s", lastError))
	}
	if guidance != "" {
		parts = append(parts, fmt.Sprintf("Human guidance:\n%s", guidance))
	}
	return strings.Join(parts, "\n\n")
}

func (c *Coordinator) recordRecoveryResume(ctx context.Context, resumedExec *blueprint.Execution, blocked *domain.Attempt, fixContext, guidance string) {
	if c.attempts == nil || blocked == nil {
		return
	}
	resume := &domain.Attempt{
		ObjectiveID:        resumedExec.ObjectiveID,
		ExecutionID:        resumedExec.ID,
		StreamID:           resumedExec.StreamID,
		StepID:             resumedExec.CurrentStep,
		AttemptNumber:      blocked.AttemptNumber + 1,
		MaxAttempts:        blocked.MaxAttempts,
		FailureKind:        blocked.FailureKind,
		Action:             blocked.Action,
		Status:             domain.AttemptStatusRunning,
		ErrorSummary:       blocked.ErrorSummary,
		FixContext:         fixContext,
		HumanGuidance:      guidance,
		TriggeredByAttempt: blocked.ID,
	}
	if err := c.attempts.Create(ctx, resume); err != nil {
		c.logger.Warn("failed to record recovery resume attempt",
			"execution_id", resumedExec.ID,
			"stream_id", resumedExec.StreamID,
			"error", err,
		)
		return
	}
	if c.eventBus != nil {
		c.eventBus.Emit(domain.EventRecoveryResumed, resumedExec.ObjectiveID, resumedExec.StreamID, "",
			"attempt_id", resume.ID,
			"triggered_by_attempt_id", blocked.ID,
			"execution_id", resumedExec.ID,
			"guidance_provided", guidance != "",
		)
	}
}
