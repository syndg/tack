package planner

import (
	"context"
	"fmt"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
)

// RunController is the run-boundary surface the planning workflow needs.
// runs.Service satisfies this interface.
type RunController interface {
	Start(ctx context.Context, objectiveID string) (domain.Snapshot, error)
	Command(ctx context.Context, runID string, cmd domain.Command) (domain.Snapshot, error)
}

// BindRunController connects the planning workflow to the run boundary.
// This is optional for tests that exercise planning in isolation.
func (s *Service) BindRunController(runStore *db.RunStore, controller RunController) {
	s.runStore = runStore
	s.runController = controller
}

// StartSimpleExecution creates a simple objective/plan workflow and starts
// execution through the run boundary when auto-approved.
func (s *Service) StartSimpleExecution(ctx context.Context, description string, opts SimpleOpts) (*domain.Objective, *domain.Plan, error) {
	obj, plan, err := s.StartSimple(ctx, description, opts)
	if err != nil {
		return nil, nil, err
	}
	if !opts.AutoApprove || s.runController == nil {
		return obj, plan, nil
	}

	if _, err := s.runController.Start(ctx, obj.ID); err != nil {
		return nil, nil, fmt.Errorf("starting run for simple objective: %w", err)
	}

	obj, err = s.objectives.Get(ctx, obj.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("refreshing simple objective after start: %w", err)
	}
	plan, err = s.plans.Get(ctx, plan.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("refreshing simple plan after start: %w", err)
	}
	return obj, plan, nil
}

// ApprovePlan advances a plan through approval and resumes any blocked run for
// the objective via the run boundary.
func (s *Service) ApprovePlan(ctx context.Context, planID string) (*domain.Plan, error) {
	if err := s.lifecycle.ApprovePlan(ctx, planID); err != nil {
		return nil, err
	}

	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("refreshing plan after approve: %w", err)
	}

	if s.runStore != nil && s.runController != nil {
		if run, err := s.runStore.GetByObjective(ctx, plan.ObjectiveID); err == nil {
			if _, err := s.runController.Command(ctx, run.ID, domain.Command{Kind: domain.CommandApprove}); err != nil {
				return nil, fmt.Errorf("resuming run after plan approval: %w", err)
			}
			s.logger.Info("auto-resumed run after plan approval",
				"run_id", run.ID, "plan_id", planID)
		}
	}

	return plan, nil
}

// RejectPlan rejects a plan and returns the refreshed plan state.
func (s *Service) RejectPlan(ctx context.Context, planID string) (*domain.Plan, error) {
	if err := s.lifecycle.RejectPlan(ctx, planID); err != nil {
		return nil, err
	}

	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("refreshing plan after reject: %w", err)
	}
	return plan, nil
}
