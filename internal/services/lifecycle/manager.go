package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

// validTransitions maps each objective status to the statuses it may transition to.
var validTransitions = map[domain.ObjectiveStatus][]domain.ObjectiveStatus{
	domain.ObjectiveStatusPlanning:  {domain.ObjectiveStatusApproved, domain.ObjectiveStatusFailed},
	domain.ObjectiveStatusApproved:  {domain.ObjectiveStatusExecuting, domain.ObjectiveStatusFailed},
	domain.ObjectiveStatusExecuting: {domain.ObjectiveStatusCompleted, domain.ObjectiveStatusPartial, domain.ObjectiveStatusFailed},
	domain.ObjectiveStatusPartial:   {domain.ObjectiveStatusCompleted}, // after retrying failed streams
	domain.ObjectiveStatusFailed:    {domain.ObjectiveStatusPlanning},
}

// Manager enforces objective state transitions and coordinates
// plan/stream status propagation.
type Manager struct {
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	agents     *db.AgentStore
	eventBus   *events.PersistentBus
	logger     *slog.Logger
}

// New creates a new lifecycle Manager.
func New(
	objectives *db.ObjectiveStore,
	plans *db.PlanStore,
	streams *db.StreamStore,
	agents *db.AgentStore,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		objectives: objectives,
		plans:      plans,
		streams:    streams,
		agents:     agents,
		eventBus:   eventBus,
		logger:     logger,
	}
}

// IsValidTransition checks if a status transition is allowed.
func IsValidTransition(from, to domain.ObjectiveStatus) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}
	return false
}

// Transition moves an objective to a new status if the transition is valid.
// Valid transitions:
//
//	planning  → approved, failed
//	approved  → executing, failed
//	executing → completed, partial, failed
//	partial   → completed
//	failed    → planning (retry)
//
// Publishes an EventObjectiveUpdated on success.
func (m *Manager) Transition(ctx context.Context, objectiveID string, to domain.ObjectiveStatus) error {
	obj, err := m.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective: %w", err)
	}

	if !IsValidTransition(obj.Status, to) {
		return fmt.Errorf("invalid transition %s → %s for objective %s", obj.Status, to, objectiveID)
	}

	from := obj.Status
	if err := m.objectives.UpdateStatus(ctx, objectiveID, to); err != nil {
		return fmt.Errorf("updating objective status: %w", err)
	}

	payload, err := json.Marshal(map[string]string{
		"from": string(from),
		"to":   string(to),
	})
	if err != nil {
		m.logger.Error("failed to marshal transition payload", "error", err)
		payload = []byte("{}")
	}

	m.eventBus.Publish(domain.Event{
		Type:      domain.EventObjectiveUpdated,
		Objective: objectiveID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})

	m.logger.Info("objective transitioned", "id", objectiveID, "from", from, "to", to)
	return nil
}

// ApprovePlan approves a plan and transitions the objective to "approved".
// Sets plan status to "approved", objective status to "approved".
func (m *Manager) ApprovePlan(ctx context.Context, planID string) error {
	plan, err := m.plans.Get(ctx, planID)
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	if err := m.plans.UpdateStatus(ctx, planID, domain.PlanStatusApproved); err != nil {
		return fmt.Errorf("updating plan status to approved: %w", err)
	}

	if err := m.Transition(ctx, plan.ObjectiveID, domain.ObjectiveStatusApproved); err != nil {
		return fmt.Errorf("transitioning objective to approved: %w", err)
	}

	m.logger.Info("plan approved", "plan_id", planID, "objective_id", plan.ObjectiveID)
	return nil
}

// RejectPlan rejects a plan and returns the objective to "planning".
// Sets plan status to "failed", keeps objective in "planning" for re-plan.
func (m *Manager) RejectPlan(ctx context.Context, planID string) error {
	plan, err := m.plans.Get(ctx, planID)
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	if err := m.plans.UpdateStatus(ctx, planID, domain.PlanStatusFailed); err != nil {
		return fmt.Errorf("updating plan status to failed: %w", err)
	}

	m.logger.Info("plan rejected", "plan_id", planID, "objective_id", plan.ObjectiveID)
	return nil
}

// MarkPlanReady sets a plan to "pending_approval" and publishes an event.
// Called by the planning service when a planner agent finishes.
func (m *Manager) MarkPlanReady(ctx context.Context, planID string) error {
	plan, err := m.plans.Get(ctx, planID)
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	if err := m.plans.UpdateStatus(ctx, planID, domain.PlanStatusPendingApproval); err != nil {
		return fmt.Errorf("updating plan status to pending_approval: %w", err)
	}

	payload, err := json.Marshal(map[string]string{
		"plan_id":     planID,
		"plan_status": string(domain.PlanStatusPendingApproval),
	})
	if err != nil {
		m.logger.Error("failed to marshal plan ready payload", "error", err)
		payload = []byte("{}")
	}

	m.eventBus.Publish(domain.Event{
		Type:      domain.EventObjectiveUpdated,
		Objective: plan.ObjectiveID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})

	m.logger.Info("plan ready for approval", "plan_id", planID, "objective_id", plan.ObjectiveID)
	return nil
}
