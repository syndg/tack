package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/recovery"
	"github.com/syndg/tack/internal/services/agents"
)

const retryContextMetadataKey = "retry_context"

type recoveryAttemptInput struct {
	ProjectID      string
	ObjectiveID    string
	ExecutionID    string
	StreamID       string
	StepID         string
	CurrentAttempt int
	FailureKind    domain.FailureKind
	ErrorSummary   string
	FixContext     string
	HumanGuidance  string
	MaxAttempts    int
}

func decodeRetryContext(metadata map[string]string) *agents.RetryContext {
	if len(metadata) == 0 || metadata[retryContextMetadataKey] == "" {
		return nil
	}
	var ctx agents.RetryContext
	if err := json.Unmarshal([]byte(metadata[retryContextMetadataKey]), &ctx); err != nil {
		return nil
	}
	if ctx.FailureKind == "" && ctx.LastError == "" && ctx.HumanGuidance == "" {
		return nil
	}
	return &ctx
}

func encodeRetryContext(ctx *agents.RetryContext) string {
	if ctx == nil {
		return ""
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		return ""
	}
	return string(data)
}

func retryContextFixContext(ctx *agents.RetryContext, fallback string) string {
	if ctx == nil {
		return fallback
	}
	if strings.TrimSpace(ctx.LastError) != "" {
		return ctx.LastError
	}
	return fallback
}

func mergeStepMetadata(base map[string]string, retryCtx *agents.RetryContext) map[string]string {
	if len(base) == 0 && retryCtx == nil {
		return nil
	}
	merged := make(map[string]string, len(base)+1)
	for k, v := range base {
		merged[k] = v
	}
	if encoded := encodeRetryContext(retryCtx); encoded != "" {
		merged[retryContextMetadataKey] = encoded
	}
	return merged
}

func humanGuidanceFromRetryContext(exec *blueprint.Execution, stepID string) string {
	if exec == nil {
		return ""
	}
	if state := exec.StepStates[stepID]; state != nil {
		if retryCtx := decodeRetryContext(state.Metadata); retryCtx != nil {
			return strings.TrimSpace(retryCtx.HumanGuidance)
		}
	}
	return ""
}

func latestRecoveryContextForAgentStep(ctx context.Context, attempts *db.AttemptStore, exec *blueprint.Execution, stepID string, metadata map[string]string) *agents.RetryContext {
	if retryCtx := decodeRetryContext(metadata); retryCtx != nil {
		return retryCtx
	}
	if attempts == nil || exec == nil {
		return nil
	}
	ledger, err := attempts.ListByExecution(ctx, exec.ID)
	if err != nil {
		return nil
	}
	for i := len(ledger) - 1; i >= 0; i-- {
		attempt := ledger[i]
		if attempt.Action != domain.RecoveryActionRerunPreviousAgent && attempt.Action != domain.RecoveryActionRetrySameStep {
			continue
		}
		if attempt.Action == domain.RecoveryActionRetrySameStep && attempt.StepID != stepID {
			continue
		}
		return &agents.RetryContext{
			AttemptNumber: attempt.AttemptNumber,
			MaxAttempts:   attempt.MaxAttempts,
			FailureKind:   attempt.FailureKind,
			LastError:     attempt.ErrorSummary,
			HumanGuidance: attempt.HumanGuidance,
		}
	}
	return nil
}

func (c *Coordinator) recordRecoveryAttempt(ctx context.Context, in recoveryAttemptInput, cfg recovery.Config, override recovery.StepOverride) (domain.Attempt, recovery.Decision) {
	policy := recovery.ResolvePolicy(cfg, override)
	attemptNumber := 1
	priorFixContext := ""
	if in.CurrentAttempt > 0 {
		attemptNumber = in.CurrentAttempt + 1
	}
	if c.attempts != nil {
		if existing, err := c.attempts.ListByExecution(ctx, in.ExecutionID); err == nil {
			for _, attempt := range existing {
				if attempt.StepID == in.StepID && attempt.FailureKind == in.FailureKind {
					if attempt.AttemptNumber >= attemptNumber {
						attemptNumber = attempt.AttemptNumber + 1
					}
					if strings.TrimSpace(attempt.FixContext) != "" {
						priorFixContext = strings.TrimSpace(attempt.FixContext)
					} else if strings.TrimSpace(attempt.ErrorSummary) != "" {
						priorFixContext = strings.TrimSpace(attempt.ErrorSummary)
					}
				}
			}
		}
	}
	fixContext := strings.TrimSpace(in.FixContext)
	if fixContext == "" {
		fixContext = in.ErrorSummary
	}
	if priorFixContext != "" {
		fixContext = mergeReviewFixContext(fixContext, priorFixContext)
	}
	decision := recovery.Decide(in.FailureKind, attemptNumber, policy)
	status := domain.AttemptStatusRecorded
	if decision.Action == domain.RecoveryActionAskHumanThenResume {
		status = domain.AttemptStatusBlocked
		if attemptNumber >= policy.MaxAttempts {
			status = domain.AttemptStatusExhausted
		}
	}
	if decision.Action == domain.RecoveryActionFailTerminal {
		status = domain.AttemptStatusExhausted
	}
	attempt := domain.Attempt{
		ProjectID:     in.ProjectID,
		ObjectiveID:   in.ObjectiveID,
		ExecutionID:   in.ExecutionID,
		StreamID:      in.StreamID,
		StepID:        in.StepID,
		AttemptNumber: attemptNumber,
		MaxAttempts:   policy.MaxAttempts,
		FailureKind:   in.FailureKind,
		Action:        decision.Action,
		Status:        status,
		ErrorSummary:  in.ErrorSummary,
		FixContext:    fixContext,
		HumanGuidance: in.HumanGuidance,
	}
	persistenceFatal := false
	if c.attempts != nil {
		if err := c.attempts.Create(ctx, &attempt); err != nil {
			if !isExpectedShutdownError(ctx, err) {
				c.logger.Warn("failed to record recovery attempt", "execution_id", in.ExecutionID, "step_id", in.StepID, "error", err)
			}
			if ctx.Err() != nil || strings.Contains(strings.ToLower(err.Error()), "database is closed") {
				persistenceFatal = true
				decision.Action = domain.RecoveryActionFailTerminal
				attempt.Status = domain.AttemptStatusFailed
			}
		}
	}
	if c.eventBus != nil && !persistenceFatal {
		summary := fmt.Sprintf("%s -> %s (%d/%d)", in.FailureKind, decision.Action, attempt.AttemptNumber, attempt.MaxAttempts)
		c.eventBus.Emit(domain.EventRecoveryAttempt, in.ObjectiveID, in.StreamID, "",
			"execution_id", in.ExecutionID,
			"step_id", in.StepID,
			"attempt_number", attempt.AttemptNumber,
			"max_attempts", attempt.MaxAttempts,
			"failure_kind", attempt.FailureKind,
			"action", attempt.Action,
			"summary", summary,
			"error", in.ErrorSummary,
		)
		if attempt.Status == domain.AttemptStatusBlocked || attempt.Status == domain.AttemptStatusExhausted {
			c.eventBus.Emit(domain.EventRecoveryBlocked, in.ObjectiveID, in.StreamID, "",
				"execution_id", in.ExecutionID,
				"step_id", in.StepID,
				"attempt_number", attempt.AttemptNumber,
				"max_attempts", attempt.MaxAttempts,
				"failure_kind", attempt.FailureKind,
				"action", attempt.Action,
				"summary", summary,
			)
		}
	}
	return attempt, decision
}
