package promotions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
)

var (
	ErrInvalidTarget     = errors.New("invalid promotion target")
	ErrInvalidTransition = errors.New("invalid promotion transition")
	ErrWrongProject      = errors.New("promotion source belongs to another project")
)

type CandidateStore interface {
	Get(ctx context.Context, id string) (*domain.CodificationCandidate, error)
}

type InsightStore interface {
	Get(ctx context.Context, id string) (*domain.ObjectiveInsight, error)
}

type Store interface {
	Create(ctx context.Context, record *domain.PromotionRecord) error
	GetByCandidateAndTarget(ctx context.Context, projectID, candidateID string, target domain.PromotionTarget) (*domain.PromotionRecord, error)
	GetByInsightAndTarget(ctx context.Context, projectID, insightID string, target domain.PromotionTarget) (*domain.PromotionRecord, error)
}

type Service struct {
	candidates CandidateStore
	insights   InsightStore
	store      Store
}

func NewService(candidates CandidateStore, insights InsightStore, store Store) *Service {
	return &Service{candidates: candidates, insights: insights, store: store}
}

func (s *Service) PromoteCandidate(ctx context.Context, projectID, candidateID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	if !validTarget(target) {
		return nil, ErrInvalidTarget
	}
	candidate, err := s.candidates.Get(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if candidate.ProjectID != projectID {
		return nil, ErrWrongProject
	}
	existing, err := s.store.GetByCandidateAndTarget(ctx, projectID, candidateID, target)
	if err == nil {
		if existing.Status == domain.PromotionStatusApproved {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: cannot approve %s promotion", ErrInvalidTransition, existing.Status)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	record := promotionFromCandidate(*candidate, target, domain.PromotionStatusApproved)
	if err := s.store.Create(ctx, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) PromoteSource(ctx context.Context, projectID, sourceID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	record, err := s.PromoteCandidate(ctx, projectID, sourceID, target)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return record, err
	}
	return s.PromoteInsight(ctx, projectID, sourceID, target)
}

func (s *Service) PromoteInsight(ctx context.Context, projectID, insightID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	if !validTarget(target) {
		return nil, ErrInvalidTarget
	}
	insight, err := s.insights.Get(ctx, insightID)
	if err != nil {
		return nil, err
	}
	if insight.ProjectID != projectID {
		return nil, ErrWrongProject
	}
	existing, err := s.store.GetByInsightAndTarget(ctx, projectID, insightID, target)
	if err == nil {
		if existing.Status == domain.PromotionStatusApproved {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: cannot approve %s promotion", ErrInvalidTransition, existing.Status)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	record := promotionFromInsight(*insight, target, domain.PromotionStatusApproved)
	if err := s.store.Create(ctx, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) RejectCandidate(ctx context.Context, projectID, candidateID string) (*domain.PromotionRecord, error) {
	candidate, err := s.candidates.Get(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if candidate.ProjectID != projectID {
		return nil, ErrWrongProject
	}
	target := domain.PromotionTargetCodification
	existing, err := s.store.GetByCandidateAndTarget(ctx, projectID, candidateID, target)
	if err == nil {
		if existing.Status == domain.PromotionStatusRejected {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: cannot reject %s promotion", ErrInvalidTransition, existing.Status)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	record := promotionFromCandidate(*candidate, target, domain.PromotionStatusRejected)
	if err := s.store.Create(ctx, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) RejectSource(ctx context.Context, projectID, sourceID string) (*domain.PromotionRecord, error) {
	record, err := s.RejectCandidate(ctx, projectID, sourceID)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return record, err
	}
	return s.RejectInsight(ctx, projectID, sourceID)
}

func (s *Service) RejectInsight(ctx context.Context, projectID, insightID string) (*domain.PromotionRecord, error) {
	insight, err := s.insights.Get(ctx, insightID)
	if err != nil {
		return nil, err
	}
	if insight.ProjectID != projectID {
		return nil, ErrWrongProject
	}
	target := domain.PromotionTargetCodification
	existing, err := s.store.GetByInsightAndTarget(ctx, projectID, insightID, target)
	if err == nil {
		if existing.Status == domain.PromotionStatusRejected {
			return existing, nil
		}
		return nil, fmt.Errorf("%w: cannot reject %s promotion", ErrInvalidTransition, existing.Status)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	record := promotionFromInsight(*insight, target, domain.PromotionStatusRejected)
	if err := s.store.Create(ctx, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func promotionFromCandidate(candidate domain.CodificationCandidate, target domain.PromotionTarget, status domain.PromotionStatus) domain.PromotionRecord {
	payload := map[string]string{}
	for k, v := range candidate.Payload {
		payload[k] = v
	}
	payload["candidate_target"] = string(candidate.Target)
	metadata := EvaluateThreshold(candidate.EvidenceCount, DefaultThresholdConfig())
	return domain.PromotionRecord{
		ProjectID:         candidate.ProjectID,
		ObjectiveID:       candidate.ObjectiveID,
		SourceCandidateID: candidate.ID,
		Target:            target,
		Status:            status,
		Confidence:        metadata.Confidence,
		SupportCount:      metadata.SupportCount,
		Summary:           firstNonEmpty(candidate.Title, candidate.Instruction),
		Detail:            candidate.Rationale,
		Payload:           payload,
	}
}

func promotionFromInsight(insight domain.ObjectiveInsight, target domain.PromotionTarget, status domain.PromotionStatus) domain.PromotionRecord {
	metadata := EvaluateThreshold(1, DefaultThresholdConfig())
	payload := map[string]string{}
	for k, v := range insight.Payload {
		if strings.Contains(strings.ToLower(k), "transcript") {
			continue
		}
		payload[k] = v
	}
	return domain.PromotionRecord{
		ProjectID:        insight.ProjectID,
		ObjectiveID:      insight.ObjectiveID,
		SourceInsightIDs: []string{insight.ID},
		Target:           target,
		Status:           status,
		Confidence:       metadata.Confidence,
		SupportCount:     metadata.SupportCount,
		Summary:          insight.Summary,
		Detail:           insight.Detail,
		Payload:          payload,
	}
}

func validTarget(target domain.PromotionTarget) bool {
	return target == domain.PromotionTargetProjectMemory || target == domain.PromotionTargetCodification
}

func confidenceForSupport(support int) float64 {
	if support <= 0 {
		return 0
	}
	if support >= 3 {
		return 1
	}
	return float64(support) / 3
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
