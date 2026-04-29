package promotions

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/codification"
	"github.com/syndg/tack/internal/db"
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

func TestOperationalMemoryV1PromotionGuardrails(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	tracked := map[string]string{
		"README.md":                          "# docs stay unchanged\n",
		".tack/rules/review.md":              "---\nscope: **/*\npriority: high\n---\nKeep reviews focused.\n",
		".tack/blueprints/build-review.yaml": "id: build-review\nname: Build Review\n",
		".github/workflows/checks.yml":       "name: checks\n",
	}
	for path, content := range tracked {
		full := filepath.Join(repoRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}

	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	project := &domain.Project{ID: "project-1", Name: "demo", RootPath: repoRoot, ConfigPath: filepath.Join(repoRoot, ".tack", "config.yaml")}
	if err := db.NewProjectStore(database.Conn()).Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objective := &domain.Objective{ProjectID: project.ID, Description: "Add auth guardrails"}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	planStore := db.NewPlanStore(database.Conn())
	plan := &domain.Plan{ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.PlanStatusDraft, QualityGates: []string{"go test ./..."}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	dossierStore := db.NewDossierStore(database.Conn())
	dossier := &domain.Dossier{ProjectID: project.ID, ObjectiveID: objective.ID, Summary: "Current repo context", RepoPriors: []domain.DossierPrior{{Kind: "rule", Title: "review", Detail: "Keep reviews focused."}}}
	if err := dossierStore.Upsert(ctx, dossier); err != nil {
		t.Fatalf("Upsert dossier: %v", err)
	}

	candidateStore := db.NewCodificationCandidateStore(database.Conn())
	insightStore := db.NewObjectiveInsightStore(database.Conn())
	insightStore.BindCodificationStore(candidateStore)
	for i := 0; i < 3; i++ {
		if err := insightStore.Create(ctx, &domain.ObjectiveInsight{
			ProjectID:   project.ID,
			ObjectiveID: objective.ID,
			Source:      domain.InsightSourceReviewer,
			Kind:        domain.InsightKindReviewRejection,
			Summary:     "Keep auth middleware coverage explicit",
			Detail:      "Repeated review finding",
			Payload:     map[string]string{"raw_transcript": "long chat log", "source": "reviewer"},
		}); err != nil {
			t.Fatalf("Create insight %d: %v", i, err)
		}
	}
	candidates, err := candidateStore.ListByObjective(ctx, objective.ID)
	if err != nil {
		t.Fatalf("ListByObjective candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].EvidenceCount != 3 {
		t.Fatalf("candidate derivation before promotion = %#v", candidates)
	}

	promotionStore := db.NewPromotionRecordStore(database.Conn())
	service := NewService(candidateStore, insightStore, promotionStore)
	if _, err := service.PromoteCandidate(ctx, project.ID, candidates[0].ID, "global-memory"); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("global target err = %v, want ErrInvalidTarget", err)
	}
	record, err := service.PromoteCandidate(ctx, project.ID, candidates[0].ID, domain.PromotionTargetProjectMemory)
	if err != nil {
		t.Fatalf("PromoteCandidate: %v", err)
	}
	if record.Status != domain.PromotionStatusApproved || record.Target != domain.PromotionTargetProjectMemory {
		t.Fatalf("approved project-memory record = %+v", record)
	}
	if record.SupportCount != 3 || record.Confidence != 1 {
		t.Fatalf("threshold support metadata = %+v", record)
	}
	metadata := EvaluateThreshold(record.SupportCount, DefaultThresholdConfig())
	if !metadata.ThresholdEligible || metadata.AutoApprovalEnabled || metadata.AutoApproved {
		t.Fatalf("default threshold metadata = %+v", metadata)
	}

	insights, err := insightStore.ListByObjective(ctx, objective.ID, 0)
	if err != nil {
		t.Fatalf("ListByObjective insights: %v", err)
	}
	if len(insights) != 3 {
		t.Fatalf("insight capture after promotion = %d, want 3", len(insights))
	}
	derived := codification.DeriveCandidates(project.ID, objective.ID, insights)
	if len(derived) != 1 || derived[0].EvidenceCount != 3 {
		t.Fatalf("objective-local candidate derivation after promotion = %#v", derived)
	}
	allPromotions, err := promotionStore.ListByObjective(ctx, objective.ID)
	if err != nil {
		t.Fatalf("ListByObjective promotions: %v", err)
	}
	if len(allPromotions) != 1 {
		t.Fatalf("promotion records = %#v, want one project-local record only", allPromotions)
	}
	if strings.Contains(strings.ToLower(allPromotions[0].Detail), "transcript") || containsTranscriptPayload(allPromotions[0].Payload) {
		t.Fatalf("promotion stored raw transcript data: %+v", allPromotions[0])
	}

	loadedPlan, err := planStore.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("Get plan: %v", err)
	}
	if !reflect.DeepEqual(loadedPlan.QualityGates, []string{"go test ./..."}) {
		t.Fatalf("quality gates mutated: %#v", loadedPlan.QualityGates)
	}
	loadedDossier, err := dossierStore.GetByObjective(ctx, objective.ID)
	if err != nil {
		t.Fatalf("Get dossier: %v", err)
	}
	if loadedDossier.Summary != dossier.Summary || !reflect.DeepEqual(loadedDossier.RepoPriors, dossier.RepoPriors) {
		t.Fatalf("dossier mutated: %+v", loadedDossier)
	}
	for path, want := range tracked {
		got, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("repo file %s mutated: %q", path, string(got))
		}
	}
}

func containsTranscriptPayload(payload map[string]string) bool {
	for key, value := range payload {
		if strings.Contains(strings.ToLower(key), "transcript") || strings.Contains(strings.ToLower(value), "transcript") || strings.Contains(value, "long chat log") {
			return true
		}
	}
	return false
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
