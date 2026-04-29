package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/domain"
)

type planResolveOptions struct {
	preferPendingApproval bool
}

func resolvePlanRef(cmd *cobra.Command, c *client.Client, raw string, opts planResolveOptions) (*domain.Plan, error) {
	plans, err := c.ListPlans(cmd.Context())
	if err != nil {
		return nil, err
	}
	objectives, err := c.ListObjectives(cmd.Context())
	if err != nil {
		return nil, err
	}
	return resolvePlanFromListWithObjectives(plans, raw, opts, objectivesByID(objectives))
}

func resolvePlanFromList(plans []domain.Plan, raw string, opts planResolveOptions) (*domain.Plan, error) {
	return resolvePlanFromListWithObjectives(plans, raw, opts, nil)
}

func resolvePlanFromListWithObjectives(plans []domain.Plan, raw string, opts planResolveOptions, objectiveByID map[string]domain.Objective) (*domain.Plan, error) {
	if len(plans) == 0 {
		return nil, fmt.Errorf("no plans found for this project")
	}

	ref := strings.TrimSpace(raw)
	if ref == "" || ref == "latest" || ref == "current" {
		return selectLatestPlan(plans, opts, objectiveByID)
	}

	if plan := matchExactPlanID(plans, ref); plan != nil {
		return plan, nil
	}
	if plan, err := matchPlanPrefix(plans, ref); plan != nil || err != nil {
		return plan, err
	}
	if plan, err := matchObjectiveRef(plans, ref); plan != nil || err != nil {
		return plan, err
	}

	return nil, fmt.Errorf("no plan matches %q", ref)
}

func selectLatestPlan(plans []domain.Plan, opts planResolveOptions, objectiveByID map[string]domain.Objective) (*domain.Plan, error) {
	if opts.preferPendingApproval {
		pending := make([]domain.Plan, 0, len(plans))
		for i := range plans {
			if plans[i].Status == domain.PlanStatusPendingApproval {
				pending = append(pending, plans[i])
			}
		}
		switch len(pending) {
		case 0:
		case 1:
			return &pending[0], nil
		default:
			return nil, multiplePendingPlansError(pending, objectiveByID)
		}
	}
	return &plans[0], nil
}

func multiplePendingPlansError(plans []domain.Plan, objectiveByID map[string]domain.Objective) error {
	lines := make([]string, 0, len(plans)+1)
	lines = append(lines, "multiple pending plans; choose one explicitly:")
	for _, plan := range plans {
		lines = append(lines, fmt.Sprintf("  %s  %s", truncateID(plan.ID), objectiveLabel(plan, objectiveByID)))
	}
	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}

func matchExactPlanID(plans []domain.Plan, ref string) *domain.Plan {
	for i := range plans {
		if plans[i].ID == ref {
			return &plans[i]
		}
	}
	return nil
}

func matchPlanPrefix(plans []domain.Plan, ref string) (*domain.Plan, error) {
	var matches []*domain.Plan
	for i := range plans {
		if strings.HasPrefix(plans[i].ID, ref) {
			matches = append(matches, &plans[i])
		}
	}
	return singlePlanMatch(matches, ref, "plan")
}

func matchObjectiveRef(plans []domain.Plan, ref string) (*domain.Plan, error) {
	byObjective := make(map[string]*domain.Plan)
	for i := range plans {
		if _, ok := byObjective[plans[i].ObjectiveID]; !ok {
			byObjective[plans[i].ObjectiveID] = &plans[i]
		}
	}

	var exact *domain.Plan
	for objectiveID, plan := range byObjective {
		if objectiveID == ref {
			exact = plan
			break
		}
	}
	if exact != nil {
		return exact, nil
	}

	var matches []*domain.Plan
	for objectiveID, plan := range byObjective {
		if strings.HasPrefix(objectiveID, ref) {
			matches = append(matches, plan)
		}
	}
	return singlePlanMatch(matches, ref, "objective")
}

func singlePlanMatch(matches []*domain.Plan, ref, kind string) (*domain.Plan, error) {
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		display := make([]string, 0, min(3, len(matches)))
		for _, plan := range matches[:min(3, len(matches))] {
			display = append(display, truncateID(plan.ID))
		}
		return nil, fmt.Errorf("%s reference %q is ambiguous; matches plans %s", kind, ref, strings.Join(display, ", "))
	}
}
