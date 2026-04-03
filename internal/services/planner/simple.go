package planner

import (
	"context"
	"fmt"

	"github.com/syndg/tack/internal/domain"
)

// SimpleOpts configures the legacy single-plan helper.
type SimpleOpts struct {
	Blueprint    string   // optional blueprint override; empty uses the coordinator default blueprint later
	AutoApprove  bool     // skip approval step
	QualityGates []string // override quality gates (empty = use config defaults)
}

// StartSimple creates an objective, generates a single-stream plan,
// and optionally auto-approves it.
// Returns the created objective and plan.
//
// Flow:
//  1. Create objective with description
//  2. Create single-stream plan via CreateSimplePlan
//  3. If AutoApprove, approve the plan immediately
//  4. Publish events
func (s *Service) StartSimple(ctx context.Context, description string, opts SimpleOpts) (*domain.Objective, *domain.Plan, error) {
	obj := &domain.Objective{
		Description: description,
		Blueprint:   opts.Blueprint,
		Status:      domain.ObjectiveStatusPlanning,
	}

	if err := s.objectives.Create(ctx, obj); err != nil {
		return nil, nil, fmt.Errorf("creating objective: %w", err)
	}

	s.eventBus.Emit(domain.EventObjectiveCreated, obj.ID, "", "",
		"objective_id", obj.ID,
		"description", description,
		"mode", "simple",
	)

	s.logger.Info("simple objective created", "objective_id", obj.ID, "description", description)

	plan, err := s.CreateSimplePlan(ctx, obj.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("creating simple plan: %w", err)
	}

	// Apply quality gates override if provided.
	if len(opts.QualityGates) > 0 {
		plan.QualityGates = opts.QualityGates
		if err := s.plans.Update(ctx, plan); err != nil {
			return nil, nil, fmt.Errorf("applying quality gates: %w", err)
		}
	}

	if opts.AutoApprove {
		if err := s.lifecycle.ApprovePlan(ctx, plan.ID); err != nil {
			return nil, nil, fmt.Errorf("auto-approving plan: %w", err)
		}

		// Refresh plan to return accurate status after approval.
		plan, err = s.plans.Get(ctx, plan.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("refreshing plan after auto-approve: %w", err)
		}

		// Refresh objective to reflect the approved status.
		obj, err = s.objectives.Get(ctx, obj.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("refreshing objective after auto-approve: %w", err)
		}

		s.logger.Info("simple plan auto-approved", "plan_id", plan.ID, "objective_id", obj.ID)
	}

	return obj, plan, nil
}
