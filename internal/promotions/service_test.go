package promotions

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestServicePromoteCandidateCreatesApprovedRecord(t *testing.T) {
	ctx := context.Background()
	candidates := &fakeCandidates{candidate: &domain.CodificationCandidate{ID: "candidate-1", ProjectID: "project-1", ObjectiveID: "obj-1", Target: domain.CodificationTargetReviewCheck, Title: "Review check", Instruction: "Keep tests focused", Rationale: "Repeated review finding", EvidenceCount: 2, Payload: map[string]string{"kind_counts": "review_rejection:2"}}}
	store := newFakePromotionStore()
	service := NewService(candidates, nil, store)

	record, err := service.PromoteCandidate(ctx, "project-1", "candidate-1", domain.PromotionTargetProjectMemory)
	if err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}
	if record.Status != domain.PromotionStatusApproved || record.Target != domain.PromotionTargetProjectMemory {
		t.Fatalf("record status/target = %s/%s", record.Status, record.Target)
	}
	if record.SourceCandidateID != "candidate-1" || record.SupportCount != 2 || record.Confidence == 0 {
		t.Fatalf("record metadata = %+v", record)
	}
}

func TestEvaluateThresholdDoesNotAutoApproveByDefault(t *testing.T) {
	metadata := EvaluateThreshold(3, DefaultThresholdConfig())
	if !metadata.ThresholdEligible {
		t.Fatalf("metadata should be threshold eligible: %+v", metadata)
	}
	if metadata.AutoApprovalEnabled || metadata.AutoApproved {
		t.Fatalf("default threshold metadata auto-approved: %+v", metadata)
	}

	enabled := EvaluateThreshold(3, ThresholdConfig{SupportThreshold: 3, AutoApprovalEnabled: true})
	if !enabled.AutoApproved {
		t.Fatalf("explicit auto approval not reflected: %+v", enabled)
	}
}

func TestPromotionFromInsightPersistsRawInsightMetadata(t *testing.T) {
	insight := domain.ObjectiveInsight{ID: "insight-1", ProjectID: "project-1", ObjectiveID: "obj-1", Summary: "Use Bun", Detail: "Package tooling should use Bun", Payload: map[string]string{"source": "human", "raw_transcript": "long chat log"}}
	record := promotionFromInsight(insight, domain.PromotionTargetProjectMemory, domain.PromotionStatusApproved)

	if record.SourceCandidateID != "" || len(record.SourceInsightIDs) != 1 || record.SourceInsightIDs[0] != "insight-1" {
		t.Fatalf("record source = %+v", record)
	}
	if record.SupportCount != 1 || record.Confidence == 0 || record.Summary != insight.Summary || record.Payload["source"] != "human" {
		t.Fatalf("record metadata = %+v", record)
	}
	if _, ok := record.Payload["raw_transcript"]; ok {
		t.Fatalf("promotion stored transcript payload: %+v", record.Payload)
	}
}

func TestServicePromoteInsightCreatesApprovedRecord(t *testing.T) {
	ctx := context.Background()
	insights := &fakeInsights{insight: &domain.ObjectiveInsight{ID: "insight-1", ProjectID: "project-1", ObjectiveID: "obj-1", Summary: "Use Bun", Detail: "Use Bun for scripts", Payload: map[string]string{"source": "human"}}}
	store := newFakePromotionStore()
	service := NewService(&fakeCandidates{}, insights, store)

	record, err := service.PromoteSource(ctx, "project-1", "insight-1", domain.PromotionTargetProjectMemory)
	if err != nil {
		t.Fatalf("PromoteSource: %v", err)
	}
	if record.Status != domain.PromotionStatusApproved || record.Target != domain.PromotionTargetProjectMemory {
		t.Fatalf("record status/target = %s/%s", record.Status, record.Target)
	}
	if record.SourceCandidateID != "" || len(record.SourceInsightIDs) != 1 || record.SourceInsightIDs[0] != "insight-1" {
		t.Fatalf("record source = %+v", record)
	}
	if record.Summary != "Use Bun" || record.Detail != "Use Bun for scripts" || record.Payload["source"] != "human" {
		t.Fatalf("record content = %+v", record)
	}
}

func TestServicePromoteCandidateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	candidates := &fakeCandidates{candidate: &domain.CodificationCandidate{ID: "candidate-1", ProjectID: "project-1", ObjectiveID: "obj-1", EvidenceCount: 2}}
	store := newFakePromotionStore()
	service := NewService(candidates, nil, store)

	first, err := service.PromoteCandidate(ctx, "project-1", "candidate-1", domain.PromotionTargetCodification)
	if err != nil {
		t.Fatalf("first promote: %v", err)
	}
	second, err := service.PromoteCandidate(ctx, "project-1", "candidate-1", domain.PromotionTargetCodification)
	if err != nil {
		t.Fatalf("second promote: %v", err)
	}
	if first != second || len(store.records) != 1 {
		t.Fatalf("promotion not idempotent: first=%p second=%p records=%d", first, second, len(store.records))
	}
}

func TestServiceRejectCandidateAndInvalidTransition(t *testing.T) {
	ctx := context.Background()
	candidates := &fakeCandidates{candidate: &domain.CodificationCandidate{ID: "candidate-1", ProjectID: "project-1", ObjectiveID: "obj-1", EvidenceCount: 2}}
	store := newFakePromotionStore()
	service := NewService(candidates, nil, store)

	rejected, err := service.RejectCandidate(ctx, "project-1", "candidate-1")
	if err != nil {
		t.Fatalf("RejectCandidate: %v", err)
	}
	if rejected.Status != domain.PromotionStatusRejected || rejected.Target != domain.PromotionTargetCodification {
		t.Fatalf("rejected = %+v", rejected)
	}
	if _, err := service.PromoteCandidate(ctx, "project-1", "candidate-1", domain.PromotionTargetCodification); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("promote rejected err = %v, want ErrInvalidTransition", err)
	}
}

func TestServiceValidatesTargetAndProject(t *testing.T) {
	ctx := context.Background()
	candidates := &fakeCandidates{candidate: &domain.CodificationCandidate{ID: "candidate-1", ProjectID: "project-1", ObjectiveID: "obj-1"}}
	service := NewService(candidates, nil, newFakePromotionStore())

	if _, err := service.PromoteCandidate(ctx, "project-1", "candidate-1", "global"); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("invalid target err = %v", err)
	}
	if _, err := service.PromoteCandidate(ctx, "other-project", "candidate-1", domain.PromotionTargetCodification); !errors.Is(err, ErrWrongProject) {
		t.Fatalf("wrong project err = %v", err)
	}
}

func TestServiceRejectInsightAndInvalidTransition(t *testing.T) {
	ctx := context.Background()
	insights := &fakeInsights{insight: &domain.ObjectiveInsight{ID: "insight-1", ProjectID: "project-1", ObjectiveID: "obj-1", Summary: "Noisy learning"}}
	store := newFakePromotionStore()
	service := NewService(&fakeCandidates{}, insights, store)

	rejected, err := service.RejectSource(ctx, "project-1", "insight-1")
	if err != nil {
		t.Fatalf("RejectSource: %v", err)
	}
	if rejected.Status != domain.PromotionStatusRejected || rejected.Target != domain.PromotionTargetCodification {
		t.Fatalf("rejected = %+v", rejected)
	}
	if _, err := service.PromoteSource(ctx, "project-1", "insight-1", domain.PromotionTargetCodification); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("promote rejected err = %v, want ErrInvalidTransition", err)
	}
}

func TestServiceValidatesInsightTargetAndProject(t *testing.T) {
	ctx := context.Background()
	insights := &fakeInsights{insight: &domain.ObjectiveInsight{ID: "insight-1", ProjectID: "project-1", ObjectiveID: "obj-1"}}
	service := NewService(&fakeCandidates{}, insights, newFakePromotionStore())

	if _, err := service.PromoteSource(ctx, "project-1", "insight-1", "global"); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("invalid target err = %v", err)
	}
	if _, err := service.PromoteSource(ctx, "other-project", "insight-1", domain.PromotionTargetCodification); !errors.Is(err, ErrWrongProject) {
		t.Fatalf("wrong project err = %v", err)
	}
}

type fakeCandidates struct {
	candidate *domain.CodificationCandidate
}

func (f *fakeCandidates) Get(context.Context, string) (*domain.CodificationCandidate, error) {
	if f.candidate == nil {
		return nil, sql.ErrNoRows
	}
	return f.candidate, nil
}

type fakeInsights struct {
	insight *domain.ObjectiveInsight
}

func (f *fakeInsights) Get(context.Context, string) (*domain.ObjectiveInsight, error) {
	if f.insight == nil {
		return nil, sql.ErrNoRows
	}
	return f.insight, nil
}

type fakePromotionStore struct {
	records map[string]*domain.PromotionRecord
}

func newFakePromotionStore() *fakePromotionStore {
	return &fakePromotionStore{records: map[string]*domain.PromotionRecord{}}
}

func (f *fakePromotionStore) Create(_ context.Context, record *domain.PromotionRecord) error {
	key := string(record.Target)
	if len(record.SourceInsightIDs) > 0 {
		key = record.SourceInsightIDs[0] + ":" + key
	}
	f.records[key] = record
	return nil
}

func (f *fakePromotionStore) GetByCandidateAndTarget(_ context.Context, _ string, _ string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	record := f.records[string(target)]
	if record == nil {
		return nil, sql.ErrNoRows
	}
	return record, nil
}

func (f *fakePromotionStore) GetByInsightAndTarget(_ context.Context, _ string, insightID string, target domain.PromotionTarget) (*domain.PromotionRecord, error) {
	record := f.records[insightID+":"+string(target)]
	if record == nil {
		return nil, sql.ErrNoRows
	}
	return record, nil
}
