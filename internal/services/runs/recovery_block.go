package runs

import (
	"context"

	"github.com/syndg/tack/internal/domain"
)

func (s *Service) hasActiveRecoveryBlock(ctx context.Context, objectiveID string) bool {
	_, ok := s.activeRecoveryBlock(ctx, objectiveID)
	return ok
}

func (s *Service) activeRecoveryBlock(ctx context.Context, objectiveID string) (*domain.BlockedState, bool) {
	if s.attempts == nil {
		return nil, false
	}

	plan, err := s.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return nil, false
	}
	streams, err := s.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return nil, false
	}

	var latestStreamID string
	var latest domain.Attempt
	found := false
	for i := range streams {
		attempt, ok := s.latestRecoveryBlockAttempt(ctx, streams[i].ID)
		if !ok {
			continue
		}
		if !found || latest.CreatedAt.Before(attempt.CreatedAt) {
			latest = attempt
			latestStreamID = streams[i].ID
			found = true
		}
	}
	if !found {
		return nil, false
	}

	reason := latest.ErrorSummary
	if reason == "" {
		reason = "retry budget exhausted; awaiting human guidance"
	}

	return &domain.BlockedState{
		Kind:      "recovery",
		Reason:    reason,
		StreamID:  latestStreamID,
		AttemptID: latest.ID,
	}, true
}

func (s *Service) latestRecoveryBlockAttempt(ctx context.Context, streamID string) (domain.Attempt, bool) {
	if streamID == "" || s.attempts == nil {
		return domain.Attempt{}, false
	}
	attempts, err := s.attempts.ListByStream(ctx, streamID)
	if err != nil || len(attempts) == 0 {
		return domain.Attempt{}, false
	}
	latest := attempts[len(attempts)-1]
	if latest.Action != domain.RecoveryActionAskHumanThenResume {
		return domain.Attempt{}, false
	}
	switch latest.Status {
	case domain.AttemptStatusBlocked, domain.AttemptStatusExhausted:
		return latest, true
	default:
		return domain.Attempt{}, false
	}
}
