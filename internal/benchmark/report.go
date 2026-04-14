package benchmark

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
)

type Report struct {
	Run        Run
	DaemonRun  *domain.Run
	Objective  *domain.Objective
	Plan       *domain.Plan
	Summary    ReportSummary
	Streams    []StreamReport
	Validation *ValidationReport
	Notes      []string
}

type ValidationReport struct {
	Command   string
	Status    string
	Summary   string
	Detail    string
	CheckedAt time.Time
}

type ReportSummary struct {
	QualityGates               []string
	TotalStreams               int
	MergedStreams              int
	FailedStreams              int
	NonTerminalStreams         int
	DistinctStreamExecutions   int
	BuilderSessions            int
	ReviewerSessions           int
	RecoveryLedgerEntries      int
	ReviewRejections           int
	AutomaticRecoveryDecisions int
	HumanEscalations           int
	HumanResumes               int
	HumanGuidanceProvided      int
	MergeAttempts              int
	HumanGuidanceRequired      bool
	StartedAt                  time.Time
	FinishedAt                 time.Time
	Duration                   time.Duration
}

type StreamReport struct {
	ID                         string
	Title                      string
	Status                     domain.StreamStatus
	ExecutionCount             int
	BuilderSessions            int
	ReviewerSessions           int
	RecoveryLedgerEntries      int
	ReviewRejections           int
	AutomaticRecoveryDecisions int
	HumanEscalations           int
	HumanResumes               int
	HumanGuidanceProvided      int
	HumanGuidanceRequired      bool
	MergeAttempts              int
	StartedAt                  time.Time
	FinishedAt                 time.Time
	Duration                   time.Duration
}

func BuildReport(dataDir string, run Run) (Report, error) {
	database, err := db.Open(dataDir)
	if err != nil {
		return Report{}, fmt.Errorf("open benchmark database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	stores := reportStores{
		objectives: db.NewObjectiveStore(database.Conn()),
		plans:      db.NewPlanStore(database.Conn()),
		streams:    db.NewStreamStore(database.Conn()),
		runs:       db.NewRunStore(database.Conn()),
		executions: db.NewExecutionStore(database.Conn()),
		attempts:   db.NewAttemptStore(database.Conn()),
		agents:     db.NewAgentStore(database.Conn()),
		merges:     db.NewMergeQueueStore(database.Conn()),
		insights:   db.NewObjectiveInsightStore(database.Conn()),
	}

	report := Report{Run: run}
	if err := resolveReportContext(ctx, &stores, &report); err != nil {
		return Report{}, err
	}
	if report.Run.ObjectiveID == "" {
		return Report{}, fmt.Errorf("benchmark run %q has no objective id", run.ID)
	}

	if err := populateReport(ctx, &stores, &report); err != nil {
		return Report{}, err
	}
	return report, nil
}

type reportStores struct {
	objectives *db.ObjectiveStore
	plans      *db.PlanStore
	streams    *db.StreamStore
	runs       *db.RunStore
	executions *db.ExecutionStore
	attempts   *db.AttemptStore
	agents     *db.AgentStore
	merges     *db.MergeQueueStore
	insights   *db.ObjectiveInsightStore
}

func resolveReportContext(ctx context.Context, stores *reportStores, report *Report) error {
	if report.Run.ObjectiveID != "" {
		return nil
	}
	objectives, err := stores.objectives.List(ctx)
	if err != nil {
		return fmt.Errorf("list objectives for benchmark report inference: %w", err)
	}
	if len(objectives) != 1 {
		return nil
	}
	report.Run.ObjectiveID = objectives[0].ID
	if report.Run.ProjectID == "" {
		report.Run.ProjectID = objectives[0].ProjectID
	}
	report.Notes = append(report.Notes, "Objective inferred from single objective present in daemon data.")
	return nil
}

func populateReport(ctx context.Context, stores *reportStores, report *Report) error {
	objective, err := stores.objectives.Get(ctx, report.Run.ObjectiveID)
	if err != nil {
		if !isReportNotFound(err) {
			return fmt.Errorf("load objective: %w", err)
		}
	} else {
		report.Objective = objective
		if report.Run.ProjectID == "" {
			report.Run.ProjectID = objective.ProjectID
		}
	}

	daemonRun, err := stores.runs.GetByObjective(ctx, report.Run.ObjectiveID)
	if err != nil {
		if !isReportNotFound(err) {
			return fmt.Errorf("load daemon run: %w", err)
		}
	} else {
		report.DaemonRun = daemonRun
		if report.Run.RunID == "" {
			report.Run.RunID = daemonRun.ID
		}
	}

	plan, err := stores.plans.GetByObjective(ctx, report.Run.ObjectiveID)
	if err != nil {
		if !isReportNotFound(err) {
			return fmt.Errorf("load plan: %w", err)
		}
	} else {
		report.Plan = plan
		report.Summary.QualityGates = append([]string(nil), plan.QualityGates...)
	}

	var streams []domain.Stream
	if report.Plan != nil {
		streams, err = stores.streams.ListByPlan(ctx, report.Plan.ID)
		if err != nil {
			return fmt.Errorf("list streams: %w", err)
		}
	}

	topExec, err := stores.executions.GetByObjective(ctx, report.Run.ObjectiveID)
	if err != nil && !isReportNotFound(err) {
		return fmt.Errorf("load top execution: %w", err)
	}
	var subExecutions []blueprint.Execution
	if err == nil {
		subExecutions, err = stores.executions.ListByParent(ctx, topExec.ID)
		if err != nil {
			return fmt.Errorf("list stream executions: %w", err)
		}
	}

	attemptsByStream := map[string][]domain.Attempt{}
	for _, stream := range streams {
		attempts, err := stores.attempts.ListByStream(ctx, stream.ID)
		if err != nil {
			return fmt.Errorf("list attempts for stream %s: %w", stream.ID, err)
		}
		attemptsByStream[stream.ID] = attempts
	}

	agentSessions, err := stores.agents.ListByObjective(ctx, report.Run.ObjectiveID)
	if err != nil {
		return fmt.Errorf("list agent sessions: %w", err)
	}
	mergeEntries, err := stores.merges.ListByObjective(ctx, report.Run.ObjectiveID)
	if err != nil {
		return fmt.Errorf("list merge entries: %w", err)
	}
	insights, err := stores.insights.ListByObjective(ctx, report.Run.ObjectiveID, 0)
	if err != nil {
		return fmt.Errorf("list benchmark insights: %w", err)
	}

	executionsByStream := map[string][]blueprint.Execution{}
	executionIDsByStream := map[string]map[string]struct{}{}
	for _, exec := range subExecutions {
		if exec.StreamID == "" {
			continue
		}
		executionsByStream[exec.StreamID] = append(executionsByStream[exec.StreamID], exec)
		if executionIDsByStream[exec.StreamID] == nil {
			executionIDsByStream[exec.StreamID] = map[string]struct{}{}
		}
		executionIDsByStream[exec.StreamID][exec.ID] = struct{}{}
	}

	agentsByStream := map[string][]domain.AgentSession{}
	for _, session := range agentSessions {
		agentsByStream[session.StreamID] = append(agentsByStream[session.StreamID], session)
	}
	mergesByStream := map[string][]domain.MergeEntry{}
	for _, entry := range mergeEntries {
		mergesByStream[entry.StreamID] = append(mergesByStream[entry.StreamID], entry)
	}

	report.Streams = make([]StreamReport, 0, len(streams))
	for _, stream := range streams {
		streamReport := buildStreamReport(stream, executionsByStream[stream.ID], executionIDsByStream[stream.ID], agentsByStream[stream.ID], attemptsByStream[stream.ID], mergesByStream[stream.ID])
		report.Streams = append(report.Streams, streamReport)
		accumulateSummary(&report.Summary, streamReport)
	}

	if topExec != nil {
		report.Summary.StartedAt = topExec.CreatedAt
	}
	if report.Summary.StartedAt.IsZero() && report.DaemonRun != nil {
		report.Summary.StartedAt = report.DaemonRun.CreatedAt
	}
	report.Summary.FinishedAt = reportFinishedAt(report, streams, subExecutions, mergeEntries, attemptsByStream)
	if !report.Summary.StartedAt.IsZero() && !report.Summary.FinishedAt.IsZero() && !report.Summary.FinishedAt.Before(report.Summary.StartedAt) {
		report.Summary.Duration = report.Summary.FinishedAt.Sub(report.Summary.StartedAt)
	}
	if report.DaemonRun != nil && report.DaemonRun.Status == domain.RunStatusActive && report.Summary.MergedStreams == report.Summary.TotalStreams && report.Summary.TotalStreams > 0 {
		report.Notes = append(report.Notes, "Daemon run row is still active; completion time inferred from latest merged stream activity.")
	}
	if validation := buildValidationReport(report.Run.BenchmarkID, insights); validation != nil {
		report.Validation = validation
	}
	sort.Slice(report.Streams, func(i, j int) bool {
		return report.Streams[i].StartedAt.Before(report.Streams[j].StartedAt)
	})
	return nil
}

func buildValidationReport(benchmarkID string, insights []domain.ObjectiveInsight) *ValidationReport {
	spec, ok := FindSpec(benchmarkID)
	if !ok || strings.TrimSpace(spec.Validation) == "" {
		return nil
	}
	report := &ValidationReport{
		Command: spec.Validation,
		Status:  "not_run",
	}
	for _, insight := range insights {
		if insight.Source != domain.InsightSourceBenchmark {
			continue
		}
		switch insight.Kind {
		case domain.InsightKindBenchmarkValidationPassed:
			report.Status = "passed"
			report.Summary = strings.TrimSpace(insight.Summary)
			report.Detail = strings.TrimSpace(insight.Detail)
			report.CheckedAt = insight.CreatedAt
			return report
		case domain.InsightKindBenchmarkValidationFailed:
			report.Status = "failed"
			report.Summary = strings.TrimSpace(insight.Summary)
			report.Detail = strings.TrimSpace(insight.Detail)
			report.CheckedAt = insight.CreatedAt
			return report
		}
	}
	return report
}

func buildStreamReport(stream domain.Stream, executions []blueprint.Execution, executionIDs map[string]struct{}, agents []domain.AgentSession, attempts []domain.Attempt, merges []domain.MergeEntry) StreamReport {
	report := StreamReport{
		ID:             stream.ID,
		Title:          stream.Title,
		Status:         stream.Status,
		ExecutionCount: len(executionIDs),
		MergeAttempts:  len(merges),
	}
	for _, session := range agents {
		switch session.Role {
		case domain.AgentRoleBuilder:
			report.BuilderSessions++
		case domain.AgentRoleReviewer:
			report.ReviewerSessions++
		}
	}
	for _, attempt := range attempts {
		report.RecoveryLedgerEntries++
		if attempt.FailureKind == domain.FailureReviewRejection {
			report.ReviewRejections++
		}
		if isAutomaticRecoveryAction(attempt.Action) && attempt.TriggeredByAttempt == "" {
			report.AutomaticRecoveryDecisions++
		}
		if attempt.Action == domain.RecoveryActionAskHumanThenResume && (attempt.Status == domain.AttemptStatusBlocked || attempt.Status == domain.AttemptStatusExhausted) {
			report.HumanEscalations++
		}
		if attempt.TriggeredByAttempt != "" {
			report.HumanResumes++
			if strings.TrimSpace(attempt.HumanGuidance) != "" {
				report.HumanGuidanceProvided++
			}
		}
	}
	report.HumanGuidanceRequired = report.HumanEscalations > 0
	if len(executions) > 0 {
		report.StartedAt = executions[0].CreatedAt
		latest := executions[0].UpdatedAt
		for _, exec := range executions[1:] {
			if exec.CreatedAt.Before(report.StartedAt) {
				report.StartedAt = exec.CreatedAt
			}
			if latest.Before(exec.UpdatedAt) {
				latest = exec.UpdatedAt
			}
		}
		report.FinishedAt = latest
	}
	for _, entry := range merges {
		updatedAt := time.Unix(entry.UpdatedAt, 0)
		if report.FinishedAt.IsZero() || report.FinishedAt.Before(updatedAt) {
			report.FinishedAt = updatedAt
		}
	}
	for _, attempt := range attempts {
		if report.FinishedAt.IsZero() || report.FinishedAt.Before(attempt.CreatedAt) {
			report.FinishedAt = attempt.CreatedAt
		}
	}
	if !report.StartedAt.IsZero() && !report.FinishedAt.IsZero() && !report.FinishedAt.Before(report.StartedAt) {
		report.Duration = report.FinishedAt.Sub(report.StartedAt)
	}
	return report
}

func accumulateSummary(summary *ReportSummary, stream StreamReport) {
	summary.TotalStreams++
	summary.DistinctStreamExecutions += stream.ExecutionCount
	summary.BuilderSessions += stream.BuilderSessions
	summary.ReviewerSessions += stream.ReviewerSessions
	summary.RecoveryLedgerEntries += stream.RecoveryLedgerEntries
	summary.ReviewRejections += stream.ReviewRejections
	summary.AutomaticRecoveryDecisions += stream.AutomaticRecoveryDecisions
	summary.HumanEscalations += stream.HumanEscalations
	summary.HumanResumes += stream.HumanResumes
	summary.HumanGuidanceProvided += stream.HumanGuidanceProvided
	summary.MergeAttempts += stream.MergeAttempts
	if stream.HumanGuidanceRequired {
		summary.HumanGuidanceRequired = true
	}
	switch stream.Status {
	case domain.StreamStatusMerged:
		summary.MergedStreams++
	case domain.StreamStatusFailed:
		summary.FailedStreams++
	default:
		summary.NonTerminalStreams++
	}
}

func reportFinishedAt(report *Report, streams []domain.Stream, executions []blueprint.Execution, merges []domain.MergeEntry, attemptsByStream map[string][]domain.Attempt) time.Time {
	if report.DaemonRun != nil {
		switch report.DaemonRun.Status {
		case domain.RunStatusCompleted, domain.RunStatusFailed:
			return report.DaemonRun.UpdatedAt
		}
	}
	if report.Objective != nil {
		switch report.Objective.Status {
		case domain.ObjectiveStatusCompleted, domain.ObjectiveStatusFailed:
			return report.Objective.UpdatedAt
		}
	}
	var latest time.Time
	for _, exec := range executions {
		if latest.Before(exec.UpdatedAt) {
			latest = exec.UpdatedAt
		}
	}
	for _, entry := range merges {
		updatedAt := time.Unix(entry.UpdatedAt, 0)
		if latest.Before(updatedAt) {
			latest = updatedAt
		}
	}
	for _, stream := range streams {
		for _, attempt := range attemptsByStream[stream.ID] {
			if latest.Before(attempt.CreatedAt) {
				latest = attempt.CreatedAt
			}
		}
	}
	return latest
}

func isReportNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "no rows")
}

func isAutomaticRecoveryAction(action domain.RecoveryAction) bool {
	switch action {
	case domain.RecoveryActionRetrySameStep, domain.RecoveryActionRerunPreviousAgent, domain.RecoveryActionRestartStream, domain.RecoveryActionRetryMerge:
		return true
	default:
		return false
	}
}

func RenderReportMarkdown(report Report) string {
	var b strings.Builder
	b.WriteString("# Benchmark Telemetry\n\n")
	fmt.Fprintf(&b, "- Benchmark run: `%s`\n", report.Run.ID)
	if report.Run.RunID != "" {
		fmt.Fprintf(&b, "- Daemon run: `%s`\n", report.Run.RunID)
	}
	fmt.Fprintf(&b, "- Benchmark: `%s`\n", report.Run.BenchmarkID)
	fmt.Fprintf(&b, "- Benchmark status: `%s`\n", report.Run.Status)
	if report.DaemonRun != nil {
		fmt.Fprintf(&b, "- Daemon run status: `%s`\n", report.DaemonRun.Status)
	}
	if report.Objective != nil {
		fmt.Fprintf(&b, "- Objective status: `%s`\n", report.Objective.Status)
	}
	if report.Plan != nil {
		fmt.Fprintf(&b, "- Plan status: `%s`\n", report.Plan.Status)
	}
	b.WriteString("\n## Summary\n\n")
	fmt.Fprintf(&b, "- Streams: `%d total`, `%d merged`, `%d failed`, `%d non-terminal`\n", report.Summary.TotalStreams, report.Summary.MergedStreams, report.Summary.FailedStreams, report.Summary.NonTerminalStreams)
	fmt.Fprintf(&b, "- Stream executions: `%d`\n", report.Summary.DistinctStreamExecutions)
	fmt.Fprintf(&b, "- Builder sessions: `%d`\n", report.Summary.BuilderSessions)
	fmt.Fprintf(&b, "- Reviewer sessions: `%d`\n", report.Summary.ReviewerSessions)
	fmt.Fprintf(&b, "- Recovery ledger entries: `%d`\n", report.Summary.RecoveryLedgerEntries)
	fmt.Fprintf(&b, "- Review rejections: `%d`\n", report.Summary.ReviewRejections)
	fmt.Fprintf(&b, "- Automatic recovery decisions: `%d`\n", report.Summary.AutomaticRecoveryDecisions)
	fmt.Fprintf(&b, "- Human escalations: `%d`\n", report.Summary.HumanEscalations)
	fmt.Fprintf(&b, "- Human resumes: `%d`\n", report.Summary.HumanResumes)
	fmt.Fprintf(&b, "- Human guidance provided: `%d`\n", report.Summary.HumanGuidanceProvided)
	fmt.Fprintf(&b, "- Human guidance required: `%s`\n", yesNo(report.Summary.HumanGuidanceRequired))
	fmt.Fprintf(&b, "- Merge attempts: `%d`\n", report.Summary.MergeAttempts)
	if report.Validation != nil {
		fmt.Fprintf(&b, "- Final validation: `%s`\n", report.Validation.Status)
	}
	if !report.Summary.StartedAt.IsZero() {
		fmt.Fprintf(&b, "- Started: `%s`\n", report.Summary.StartedAt.UTC().Format(time.RFC3339))
	}
	if !report.Summary.FinishedAt.IsZero() {
		fmt.Fprintf(&b, "- Finished: `%s`\n", report.Summary.FinishedAt.UTC().Format(time.RFC3339))
	}
	if report.Summary.Duration > 0 {
		fmt.Fprintf(&b, "- Duration: `%s`\n", report.Summary.Duration.Round(time.Second))
	}
	if len(report.Summary.QualityGates) > 0 {
		b.WriteString("\n## Quality Gates\n\n")
		for _, gate := range report.Summary.QualityGates {
			fmt.Fprintf(&b, "- `%s`\n", gate)
		}
	}
	if report.Validation != nil {
		b.WriteString("\n## Final Validation\n\n")
		fmt.Fprintf(&b, "- Command: `%s`\n", report.Validation.Command)
		fmt.Fprintf(&b, "- Status: `%s`\n", report.Validation.Status)
		if !report.Validation.CheckedAt.IsZero() {
			fmt.Fprintf(&b, "- Checked: `%s`\n", report.Validation.CheckedAt.UTC().Format(time.RFC3339))
		}
		if report.Validation.Summary != "" {
			fmt.Fprintf(&b, "- Summary: %s\n", report.Validation.Summary)
		}
		if report.Validation.Detail != "" {
			fmt.Fprintf(&b, "- Detail: %s\n", report.Validation.Detail)
		}
	}
	if len(report.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, note := range report.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	b.WriteString("\n## Streams\n\n")
	for _, stream := range report.Streams {
		fmt.Fprintf(&b, "### `%s` %s\n\n", stream.ID, stream.Title)
		fmt.Fprintf(&b, "- Status: `%s`\n", stream.Status)
		fmt.Fprintf(&b, "- Stream executions: `%d`\n", stream.ExecutionCount)
		fmt.Fprintf(&b, "- Builder sessions: `%d`\n", stream.BuilderSessions)
		fmt.Fprintf(&b, "- Reviewer sessions: `%d`\n", stream.ReviewerSessions)
		fmt.Fprintf(&b, "- Recovery ledger entries: `%d`\n", stream.RecoveryLedgerEntries)
		fmt.Fprintf(&b, "- Review rejections: `%d`\n", stream.ReviewRejections)
		fmt.Fprintf(&b, "- Automatic recovery decisions: `%d`\n", stream.AutomaticRecoveryDecisions)
		fmt.Fprintf(&b, "- Human escalations: `%d`\n", stream.HumanEscalations)
		fmt.Fprintf(&b, "- Human resumes: `%d`\n", stream.HumanResumes)
		fmt.Fprintf(&b, "- Human guidance provided: `%d`\n", stream.HumanGuidanceProvided)
		fmt.Fprintf(&b, "- Human guidance required: `%s`\n", yesNo(stream.HumanGuidanceRequired))
		fmt.Fprintf(&b, "- Merge attempts: `%d`\n", stream.MergeAttempts)
		if !stream.StartedAt.IsZero() {
			fmt.Fprintf(&b, "- Started: `%s`\n", stream.StartedAt.UTC().Format(time.RFC3339))
		}
		if !stream.FinishedAt.IsZero() {
			fmt.Fprintf(&b, "- Finished: `%s`\n", stream.FinishedAt.UTC().Format(time.RFC3339))
		}
		if stream.Duration > 0 {
			fmt.Fprintf(&b, "- Duration: `%s`\n", stream.Duration.Round(time.Second))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func SyncRunValidation(dataDir string, run *Run) error {
	if run == nil || run.ObjectiveID == "" {
		return nil
	}
	report, err := BuildReport(dataDir, *run)
	if err != nil {
		return err
	}
	if report.Validation == nil {
		return nil
	}
	run.ValidationStatus = report.Validation.Status
	run.ValidationSummary = report.Validation.Summary
	if !report.Validation.CheckedAt.IsZero() {
		run.ValidationChecked = report.Validation.CheckedAt.UTC().Format(time.RFC3339)
	}
	switch report.Validation.Status {
	case "passed":
		run.ScoreStatus = "passed"
	case "failed":
		run.ScoreStatus = "failed"
	default:
		run.ScoreStatus = report.Validation.Status
	}
	return nil
}
