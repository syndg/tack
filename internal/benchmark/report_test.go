package benchmark

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
)

func TestBuildReportAggregatesStreamMetrics(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	projectRoot := t.TempDir()
	project := &domain.Project{Name: "test", RootPath: projectRoot, ConfigPath: projectRoot}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ProjectID: project.ID, ID: "obj-1", Description: "benchmark objective", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	planStore := db.NewPlanStore(database.Conn())
	plan := &domain.Plan{ProjectID: project.ID, ID: "plan-1", ObjectiveID: objective.ID, Status: domain.PlanStatusCompleted, QualityGates: []string{"go test ./pkg/gui/... -count=1"}}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	streamStore := db.NewStreamStore(database.Conn())
	streamA := &domain.Stream{ProjectID: project.ID, ID: "stream-a", PlanID: plan.ID, Title: "Behavior", Status: domain.StreamStatusMerged}
	streamB := &domain.Stream{ProjectID: project.ID, ID: "stream-b", PlanID: plan.ID, Title: "Coverage", Status: domain.StreamStatusMerged}
	for _, stream := range []*domain.Stream{streamA, streamB} {
		if err := streamStore.Create(ctx, stream); err != nil {
			t.Fatalf("Create stream %s: %v", stream.ID, err)
		}
	}
	runStore := db.NewRunStore(database.Conn())
	daemonRun := &domain.Run{ID: "run-1", ProjectID: project.ID, ObjectiveID: objective.ID, Status: domain.RunStatusCompleted}
	if err := runStore.Create(ctx, daemonRun); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	executionStore := db.NewExecutionStore(database.Conn())
	now := time.Now().Add(-45 * time.Minute).UTC().Truncate(time.Second)
	topExec := &blueprint.Execution{ID: "exec-top", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, CurrentStep: "execute", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now, UpdatedAt: now.Add(35 * time.Minute)}
	if err := executionStore.Create(ctx, topExec); err != nil {
		t.Fatalf("Create top execution: %v", err)
	}
	for _, exec := range []*blueprint.Execution{
		{ID: "exec-a-1", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: streamA.ID, CurrentStep: "merge", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now.Add(1 * time.Minute), UpdatedAt: now.Add(10 * time.Minute)},
		{ID: "exec-b-1", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: streamB.ID, CurrentStep: "review", StepStates: map[string]*blueprint.StepState{}, Status: "failed", CreatedAt: now.Add(12 * time.Minute), UpdatedAt: now.Add(20 * time.Minute)},
		{ID: "exec-b-2", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: streamB.ID, CurrentStep: "merge", StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now.Add(22 * time.Minute), UpdatedAt: now.Add(34 * time.Minute)},
	} {
		if err := executionStore.Create(ctx, exec); err != nil {
			t.Fatalf("Create execution %s: %v", exec.ID, err)
		}
	}

	agentStore := db.NewAgentStore(database.Conn())
	for _, session := range []*domain.AgentSession{
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamA.ID, Role: domain.AgentRoleBuilder, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamA.ID, Role: domain.AgentRoleReviewer, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamB.ID, Role: domain.AgentRoleBuilder, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamB.ID, Role: domain.AgentRoleReviewer, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamB.ID, Role: domain.AgentRoleBuilder, Status: "completed"},
		{ProjectID: project.ID, ObjectiveID: objective.ID, StreamID: streamB.ID, Role: domain.AgentRoleReviewer, Status: "completed"},
	} {
		if err := agentStore.Create(ctx, session); err != nil {
			t.Fatalf("Create agent session: %v", err)
		}
	}

	attemptStore := db.NewAttemptStore(database.Conn())
	for _, attempt := range []*domain.Attempt{
		{ProjectID: project.ID, ObjectiveID: objective.ID, ExecutionID: "exec-b-1", StreamID: streamB.ID, StepID: "review", AttemptNumber: 1, MaxAttempts: 3, FailureKind: domain.FailureReviewRejection, Action: domain.RecoveryActionRerunPreviousAgent, Status: domain.AttemptStatusRecorded, CreatedAt: now.Add(20 * time.Minute)},
		{ProjectID: project.ID, ObjectiveID: objective.ID, ExecutionID: "exec-b-1", StreamID: streamB.ID, StepID: "review", AttemptNumber: 2, MaxAttempts: 3, FailureKind: domain.FailureReviewRejection, Action: domain.RecoveryActionAskHumanThenResume, Status: domain.AttemptStatusExhausted, CreatedAt: now.Add(21 * time.Minute)},
		{ProjectID: project.ID, ObjectiveID: objective.ID, ExecutionID: "exec-b-2", StreamID: streamB.ID, StepID: "build", AttemptNumber: 3, MaxAttempts: 3, FailureKind: domain.FailureReviewRejection, Action: domain.RecoveryActionAskHumanThenResume, Status: domain.AttemptStatusRunning, HumanGuidance: "tighten coverage", TriggeredByAttempt: "resume-trigger", CreatedAt: now.Add(22 * time.Minute)},
	} {
		if err := attemptStore.Create(ctx, attempt); err != nil {
			t.Fatalf("Create attempt: %v", err)
		}
	}

	mergeStore := db.NewMergeQueueStore(database.Conn())
	for _, entry := range []*domain.MergeEntry{
		{ID: "merge-a", ProjectID: project.ID, StreamID: streamA.ID, PlanID: plan.ID, ObjectiveID: objective.ID, Branch: "branch-a", Status: domain.MergeStatusMerged, CreatedAt: now.Add(10 * time.Minute).Unix(), UpdatedAt: now.Add(11 * time.Minute).Unix()},
		{ID: "merge-b", ProjectID: project.ID, StreamID: streamB.ID, PlanID: plan.ID, ObjectiveID: objective.ID, Branch: "branch-b", Status: domain.MergeStatusMerged, CreatedAt: now.Add(34 * time.Minute).Unix(), UpdatedAt: now.Add(35 * time.Minute).Unix()},
	} {
		if err := mergeStore.Enqueue(ctx, entry); err != nil {
			t.Fatalf("Enqueue merge entry: %v", err)
		}
		if err := mergeStore.UpdateStatus(ctx, entry.ID, entry.Status, 0, "", ""); err != nil {
			t.Fatalf("Update merge entry: %v", err)
		}
	}
	insightStore := db.NewObjectiveInsightStore(database.Conn())
	candidateStore := db.NewCodificationCandidateStore(database.Conn())
	insightStore.BindCodificationStore(candidateStore)
	validationInsight := &domain.ObjectiveInsight{
		ObjectiveID: objective.ID,
		Source:      domain.InsightSourceBenchmark,
		Kind:        domain.InsightKindBenchmarkValidationPassed,
		Summary:     "Final benchmark validation passed.",
		Detail:      "integration ok",
		CreatedAt:   now.Add(36 * time.Minute),
	}
	if err := insightStore.Create(ctx, validationInsight); err != nil {
		t.Fatalf("Create validation insight: %v", err)
	}
	for _, insight := range []*domain.ObjectiveInsight{
		{ObjectiveID: objective.ID, StreamID: streamB.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "cover command navigation", Detail: "review found missing edge coverage", CreatedAt: now.Add(37 * time.Minute)},
		{ObjectiveID: objective.ID, StreamID: streamB.ID, Source: domain.InsightSourceReviewer, Kind: domain.InsightKindReviewRejection, Summary: "cover command navigation", Detail: "review found missing edge coverage", CreatedAt: now.Add(38 * time.Minute)},
	} {
		if err := insightStore.Create(ctx, insight); err != nil {
			t.Fatalf("Create memory insight: %v", err)
		}
	}
	candidates, err := candidateStore.ListByObjective(ctx, objective.ID)
	if err != nil {
		t.Fatalf("List candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	promotionStore := db.NewPromotionRecordStore(database.Conn())
	for _, promotion := range []*domain.PromotionRecord{
		{ProjectID: project.ID, ObjectiveID: objective.ID, SourceCandidateID: candidates[0].ID, Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusApproved, Confidence: 1, SupportCount: 2, Summary: "Promote repeated review check", CreatedAt: now.Add(39 * time.Minute)},
		{ProjectID: project.ID, ObjectiveID: objective.ID, SourceInsightIDs: []string{validationInsight.ID}, Target: domain.PromotionTargetProjectMemory, Status: domain.PromotionStatusRejected, Confidence: 0.5, SupportCount: 1, Summary: "Do not preserve validation event", CreatedAt: now.Add(40 * time.Minute)},
		{ProjectID: project.ID, ObjectiveID: objective.ID, SourceInsightIDs: []string{validationInsight.ID}, Target: domain.PromotionTargetCodification, Status: domain.PromotionStatusProposed, Confidence: 0.5, SupportCount: 1, Summary: "Review validation event", CreatedAt: now.Add(41 * time.Minute)},
	} {
		if err := promotionStore.Create(ctx, promotion); err != nil {
			t.Fatalf("Create promotion: %v", err)
		}
	}

	report, err := BuildReport(dataDir, Run{ID: "bench-1", BenchmarkID: "lazygit.undo-basic-commit-checkout", ObjectiveID: objective.ID, ProjectID: project.ID, RunID: daemonRun.ID, Status: "completed"})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Summary.TotalStreams != 2 || report.Summary.MergedStreams != 2 {
		t.Fatalf("summary streams = %+v", report.Summary)
	}
	if report.Summary.DistinctStreamExecutions != 3 {
		t.Fatalf("stream executions = %d, want 3", report.Summary.DistinctStreamExecutions)
	}
	if report.Summary.ReviewRejections != 3 {
		t.Fatalf("review rejections = %d, want 3", report.Summary.ReviewRejections)
	}
	if report.Summary.AutomaticRecoveryDecisions != 1 {
		t.Fatalf("automatic recoveries = %d, want 1", report.Summary.AutomaticRecoveryDecisions)
	}
	if report.Summary.HumanEscalations != 1 || report.Summary.HumanResumes != 1 {
		t.Fatalf("human guidance counts = %+v", report.Summary)
	}
	if !report.Summary.HumanGuidanceRequired || report.Summary.HumanGuidanceProvided != 1 {
		t.Fatalf("human guidance summary = %+v", report.Summary)
	}
	if report.Summary.BuilderSessions != 3 || report.Summary.ReviewerSessions != 3 {
		t.Fatalf("agent session counts = %+v", report.Summary)
	}
	if report.Summary.MergeAttempts != 2 {
		t.Fatalf("merge attempts = %d, want 2", report.Summary.MergeAttempts)
	}
	if report.Summary.Duration <= 0 {
		t.Fatalf("duration = %s, want > 0", report.Summary.Duration)
	}
	if report.Validation == nil || report.Validation.Status != "passed" {
		t.Fatalf("validation = %+v, want passed", report.Validation)
	}
	if report.Memory.TotalInsights != 3 || report.Memory.CandidateClusters != 1 {
		t.Fatalf("memory insight summary = %+v", report.Memory)
	}
	if report.Memory.ProposedPromotions != 1 || report.Memory.ApprovedPromotions != 1 || report.Memory.RejectedPromotions != 1 {
		t.Fatalf("memory promotion summary = %+v", report.Memory)
	}
	if report.Memory.ProjectMemoryTargets != 1 || report.Memory.CodificationTargets != 2 {
		t.Fatalf("memory target summary = %+v", report.Memory)
	}
	streamBReport := report.Streams[1]
	if streamBReport.ExecutionCount != 2 || streamBReport.HumanEscalations != 1 || streamBReport.HumanResumes != 1 {
		t.Fatalf("stream B report = %+v", streamBReport)
	}

	markdown := RenderReportMarkdown(report)
	for _, needle := range []string{
		"# Benchmark Telemetry",
		"- Human escalations: `1`",
		"- Human resumes: `1`",
		"- Objective insights: `3`",
		"- Codification candidate clusters: `1`",
		"- Promotions: `1 proposed`, `1 approved`, `1 rejected`",
		"- Promotion targets: `1 project-memory`, `2 codification`",
		"- Final validation: `passed`",
		"## Final Validation",
		"### `stream-b` Coverage",
		"- Stream executions: `2`",
	} {
		if !strings.Contains(markdown, needle) {
			t.Fatalf("markdown missing %q:\n%s", needle, markdown)
		}
	}
}

func TestBuildReportHandlesEmptyMemoryReviewData(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	project := &domain.Project{Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir()}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ProjectID: project.ID, ID: "obj-empty-memory", Description: "benchmark objective", Status: domain.ObjectiveStatusCompleted}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	report, err := BuildReport(dataDir, Run{ID: "bench-empty-memory", BenchmarkID: "lazygit.command-log-nav-keybindings", ObjectiveID: objective.ID, ProjectID: project.ID, Status: "completed", ScoreStatus: "passed"})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Memory != (MemoryReviewSummary{}) {
		t.Fatalf("memory summary = %+v, want empty", report.Memory)
	}
	markdown := RenderReportMarkdown(report)
	for _, needle := range []string{
		"- Objective insights: `0`",
		"- Codification candidate clusters: `0`",
		"- Promotions: `0 proposed`, `0 approved`, `0 rejected`",
		"- Promotion targets: `0 project-memory`, `0 codification`",
	} {
		if !strings.Contains(markdown, needle) {
			t.Fatalf("markdown missing %q:\n%s", needle, markdown)
		}
	}
}

func TestBuildReportInfersSingleObjectiveWhenRunMissingObjectiveID(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	projectRoot := t.TempDir()
	project := &domain.Project{Name: "test", RootPath: projectRoot, ConfigPath: projectRoot}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ProjectID: project.ID, ID: "obj-only", Description: "only objective", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}

	report, err := BuildReport(dataDir, Run{ID: "bench-2", BenchmarkID: "lazygit.command-log-nav-keybindings", Status: "planned"})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Run.ObjectiveID != objective.ID {
		t.Fatalf("objective inference = %q, want %q", report.Run.ObjectiveID, objective.ID)
	}
	if len(report.Notes) == 0 || !strings.Contains(report.Notes[0], "Objective inferred") {
		t.Fatalf("notes = %#v, want inference note", report.Notes)
	}
}

func TestBuildReportSortsPendingStreamsAfterStartedStreams(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	project := &domain.Project{Name: "test", RootPath: t.TempDir(), ConfigPath: t.TempDir()}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ProjectID: project.ID, ID: "obj-sort", Description: "benchmark objective", Status: domain.ObjectiveStatusExecuting}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	planStore := db.NewPlanStore(database.Conn())
	plan := &domain.Plan{ProjectID: project.ID, ID: "plan-sort", ObjectiveID: objective.ID, Status: domain.PlanStatusApproved}
	if err := planStore.Create(ctx, plan); err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	streamStore := db.NewStreamStore(database.Conn())
	started := &domain.Stream{ProjectID: project.ID, ID: "stream-started", PlanID: plan.ID, Title: "Started", Status: domain.StreamStatusMerged}
	pending := &domain.Stream{ProjectID: project.ID, ID: "stream-pending", PlanID: plan.ID, Title: "Pending", Status: domain.StreamStatusPending}
	for _, stream := range []*domain.Stream{pending, started} {
		if err := streamStore.Create(ctx, stream); err != nil {
			t.Fatalf("Create stream %s: %v", stream.ID, err)
		}
	}
	executionStore := db.NewExecutionStore(database.Conn())
	now := time.Now().UTC().Truncate(time.Second)
	topExec := &blueprint.Execution{ID: "exec-top-sort", ProjectID: project.ID, BlueprintID: "benchmark-baseline", ObjectiveID: objective.ID, CurrentStep: "execute", StepStates: map[string]*blueprint.StepState{}, Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := executionStore.Create(ctx, topExec); err != nil {
		t.Fatalf("Create top execution: %v", err)
	}
	childExec := &blueprint.Execution{ID: "exec-started", ProjectID: project.ID, BlueprintID: "benchmark-build-review", ObjectiveID: objective.ID, ParentID: topExec.ID, StreamID: started.ID, StepStates: map[string]*blueprint.StepState{}, Status: "completed", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(2 * time.Minute)}
	if err := executionStore.Create(ctx, childExec); err != nil {
		t.Fatalf("Create child execution: %v", err)
	}

	report, err := BuildReport(dataDir, Run{ID: "bench-sort", BenchmarkID: "lazygit.command-log-nav-keybindings", ObjectiveID: objective.ID, ProjectID: project.ID, Status: "executing"})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.Streams) != 2 || report.Streams[0].ID != started.ID || report.Streams[1].ID != pending.ID {
		t.Fatalf("stream order = %#v, want started before pending", report.Streams)
	}
}
