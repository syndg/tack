package planner

import (
	"context"
	"fmt"
	"slices"

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
	return s.ApprovePlanWithReason(ctx, planID, "")
}

func (s *Service) ApprovePlanWithReason(ctx context.Context, planID string, reason string) (*domain.Plan, error) {
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
	s.recordPlanInsight(ctx, plan, domain.InsightKindPlanApproval, firstNonEmptyReason(reason, "Plan approved for execution."), map[string]string{"reason": reason})

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

// UpdatePlanQualityGates replaces a plan's quality gates before execution.
func (s *Service) UpdatePlanQualityGates(ctx context.Context, planID string, qualityGates []string) (*domain.Plan, error) {
	return s.UpdatePlanQualityGatesWithReason(ctx, planID, qualityGates, "")
}

func (s *Service) UpdatePlanQualityGatesWithReason(ctx context.Context, planID string, qualityGates []string, reason string) (*domain.Plan, error) {
	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("getting plan: %w", err)
	}
	if plan.Status != domain.PlanStatusDraft && plan.Status != domain.PlanStatusPendingApproval {
		return nil, fmt.Errorf("plan %s must be draft or pending_approval to update quality gates", planID)
	}
	if slices.Equal(plan.QualityGates, qualityGates) {
		return plan, nil
	}
	plan.QualityGates = append([]string(nil), qualityGates...)
	if err := s.plans.Update(ctx, plan); err != nil {
		return nil, fmt.Errorf("updating plan quality gates: %w", err)
	}
	updated, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	s.recordPlanInsight(ctx, updated, domain.InsightKindPlanQualityGateEdit, firstNonEmptyReason(reason, "Plan quality gates updated before execution."), map[string]string{"reason": reason, "quality_gate_count": fmt.Sprintf("%d", len(updated.QualityGates))})
	return updated, nil
}

func (s *Service) recordPlanInsight(ctx context.Context, plan *domain.Plan, kind domain.ObjectiveInsightKind, summary string, payload map[string]string) {
	if s.insights == nil || plan == nil || summary == "" {
		return
	}
	if err := s.insights.Create(ctx, &domain.ObjectiveInsight{ProjectID: plan.ProjectID, ObjectiveID: plan.ObjectiveID, PlanID: plan.ID, Source: domain.InsightSourcePlanner, Kind: kind, Summary: summary, Detail: summary, Payload: payload}); err != nil {
		s.logger.Warn("recording plan insight", "plan_id", plan.ID, "kind", kind, "error", err)
	}
}

func firstNonEmptyReason(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}
