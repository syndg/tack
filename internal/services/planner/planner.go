package planner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	events "github.com/syndg/tack/internal/services/events"
	"github.com/syndg/tack/internal/services/lifecycle"
)

// Service manages the planning lifecycle for objectives.
type Service struct {
	plans               *db.PlanStore
	streams             *db.StreamStore
	objectives          *db.ObjectiveStore
	agentStore          *db.AgentStore
	lifecycle           *lifecycle.Manager
	eventBus            *events.PersistentBus
	logger              *slog.Logger
	defaultQualityGates []string
}

// New creates a new planning Service.
func New(
	plans *db.PlanStore,
	streams *db.StreamStore,
	objectives *db.ObjectiveStore,
	agentStore *db.AgentStore,
	lifecycle *lifecycle.Manager,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
	defaultQualityGates []string,
) *Service {
	return &Service{
		plans:               plans,
		streams:             streams,
		objectives:          objectives,
		agentStore:          agentStore,
		lifecycle:           lifecycle,
		eventBus:            eventBus,
		logger:              logger,
		defaultQualityGates: append([]string(nil), defaultQualityGates...),
	}
}

// CreatePlan creates a plan for an objective from planner agent output and stores it.
// 1. Parses and validates the raw plan output
// 2. Converts to domain types (Plan + Streams)
// 3. Persists plan and streams
// 4. Calls lifecycle.MarkPlanReady to set status to "pending_approval" and publish event
func (s *Service) CreatePlan(ctx context.Context, objectiveID string, agentOutput string) (*domain.Plan, error) {
	rawPlan, err := ParsePlan(agentOutput)
	if err != nil {
		return nil, fmt.Errorf("parsing plan: %w", err)
	}

	if err := ValidatePlan(rawPlan); err != nil {
		return nil, fmt.Errorf("validating plan: %w", err)
	}

	plan, streams := ToDomain(rawPlan, objectiveID)

	if err := s.plans.Create(ctx, plan); err != nil {
		return nil, fmt.Errorf("storing plan: %w", err)
	}

	for i := range streams {
		if err := s.streams.Create(ctx, &streams[i]); err != nil {
			return nil, fmt.Errorf("storing stream %q: %w", streams[i].Title, err)
		}
	}

	// Publish EventPlanCreated before marking ready.
	s.eventBus.Emit(domain.EventPlanCreated, objectiveID, "", "",
		"plan_id", plan.ID,
		"objective_id", objectiveID,
	)

	// Mark plan ready — sets status to "pending_approval" and publishes EventObjectiveUpdated.
	if err := s.lifecycle.MarkPlanReady(ctx, plan.ID); err != nil {
		return nil, fmt.Errorf("marking plan ready: %w", err)
	}

	// Refresh plan to return accurate status.
	updated, err := s.plans.Get(ctx, plan.ID)
	if err != nil {
		return nil, fmt.Errorf("refreshing plan after ready: %w", err)
	}

	s.logger.Info("plan created", "plan_id", plan.ID, "objective_id", objectiveID, "streams", len(streams))
	return updated, nil
}

// CreateSimplePlan creates a single-stream plan for simple mode.
// No planner agent needed — the objective description becomes the stream task.
// Single stream with full file scope ("**/*"), no dependencies.
// Plan status is set directly to "pending_approval".
func (s *Service) CreateSimplePlan(ctx context.Context, objectiveID string) (*domain.Plan, error) {
	obj, err := s.objectives.Get(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("getting objective: %w", err)
	}

	now := time.Now()

	qualityGates := append([]string(nil), s.defaultQualityGates...)
	if qualityGates == nil {
		qualityGates = []string{}
	}

	plan := &domain.Plan{
		ID:           uuid.New().String(),
		ObjectiveID:  objectiveID,
		Status:       domain.PlanStatusPendingApproval,
		QualityGates: qualityGates,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.plans.Create(ctx, plan); err != nil {
		return nil, fmt.Errorf("storing simple plan: %w", err)
	}

	stream := &domain.Stream{
		ID:           uuid.New().String(),
		PlanID:       plan.ID,
		Title:        obj.Description,
		Description:  obj.Description,
		FileScope:    []string{"**/*"},
		Dependencies: []string{},
		Status:       domain.StreamStatusPending,
		CreatedAt:    now,
	}

	if err := s.streams.Create(ctx, stream); err != nil {
		return nil, fmt.Errorf("storing simple stream: %w", err)
	}

	s.eventBus.Emit(domain.EventPlanCreated, objectiveID, "", "",
		"plan_id", plan.ID,
		"objective_id", objectiveID,
		"mode", "simple",
	)

	s.logger.Info("simple plan created", "plan_id", plan.ID, "objective_id", objectiveID)
	return plan, nil
}

// GetPlanWithStreams retrieves a plan and its streams.
func (s *Service) GetPlanWithStreams(ctx context.Context, planID string) (*domain.Plan, []domain.Stream, error) {
	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, nil, fmt.Errorf("getting plan: %w", err)
	}

	streams, err := s.streams.ListByPlan(ctx, planID)
	if err != nil {
		return nil, nil, fmt.Errorf("listing streams for plan: %w", err)
	}

	return plan, streams, nil
}

// GetPlanByObjective retrieves the plan for an objective along with its streams.
func (s *Service) GetPlanByObjective(ctx context.Context, objectiveID string) (*domain.Plan, []domain.Stream, error) {
	plan, err := s.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return nil, nil, fmt.Errorf("getting plan by objective: %w", err)
	}

	streams, err := s.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("listing streams for objective plan: %w", err)
	}

	return plan, streams, nil
}

// UpdateStream updates a stream's fields (for plan editing before approval).
func (s *Service) UpdateStream(ctx context.Context, stream *domain.Stream) error {
	if err := s.streams.Update(ctx, stream); err != nil {
		return fmt.Errorf("updating stream: %w", err)
	}
	return nil
}
