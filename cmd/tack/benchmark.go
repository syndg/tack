package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/syndg/tack/internal/benchmark"
	"github.com/syndg/tack/internal/client"
	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/domain"
)

var benchmarkRunMode string
var benchmarkRunBlueprint string
var benchmarkPrepareSource string
var benchmarkPrepareWorkspace string
var benchmarkPrepareBaseline string
var benchmarkExecuteWorkspace string
var benchmarkSupersedeReason string
var benchmarkReportWrite bool
var benchmarkReportOut string
var benchmarkPlanQualityGateWait = 10 * time.Minute
var benchmarkPlanQualityGatePollInterval = 2 * time.Second

func benchmarkStatusFromRunSnapshot(status domain.RunStatus) string {
	switch status {
	case domain.RunStatusActive:
		return "executing"
	case domain.RunStatusBlocked:
		return "waiting_human"
	case domain.RunStatusCompleted:
		return "completed"
	case domain.RunStatusPartial:
		return "partial"
	case domain.RunStatusFailed:
		return "failed"
	default:
		return string(status)
	}
}

func syncBenchmarkRun(cmd *cobra.Command, cfg *config.Config, run *benchmark.Run) error {
	if run == nil || run.ObjectiveID == "" {
		return nil
	}
	switch run.Status {
	case "superseded", "partial":
		return nil
	case "completed":
		if run.StatusReason == "" {
			return nil
		}
	case "failed":
		if run.StatusReason != "underlying objective run not found" {
			return nil
		}
	}
	c, err := newDaemonClient(cmd, false)
	if err != nil {
		return nil
	}
	if run.ProjectID != "" {
		c.SetProjectID(run.ProjectID)
	}
	snap, err := c.ObjectiveRunSnapshot(cmd.Context(), run.ObjectiveID)
	if err != nil {
		if (run.Status == "executing" || run.Status == "waiting_human") && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")) {
			run.Status = "failed"
			run.StatusReason = "underlying objective run not found"
			_ = benchmark.UpdateRun(cfg.Daemon.DataDir, *run)
		}
		return nil
	}
	newStatus := benchmarkStatusFromRunSnapshot(snap.Status)
	if run.RunID != snap.RunID || run.Status != newStatus || (newStatus != "failed" && run.StatusReason != "") {
		run.RunID = snap.RunID
		run.Status = newStatus
		if newStatus != "failed" {
			run.StatusReason = ""
		}
		_ = benchmark.UpdateRun(cfg.Daemon.DataDir, *run)
	}
	return nil
}

func applyBenchmarkPlanQualityGates(ctx context.Context, c *client.Client, objectiveID string, qualityGates []string) error {
	planResp, err := waitForBenchmarkPlan(ctx, c, objectiveID)
	if err != nil {
		return err
	}
	if len(qualityGates) == 0 {
		return nil
	}
	if _, err := c.UpdatePlanQualityGates(ctx, planResp.Plan.ID, qualityGates); err != nil {
		return fmt.Errorf("applying benchmark quality gates: %w", err)
	}
	return nil
}

func waitForBenchmarkPlan(ctx context.Context, c *client.Client, objectiveID string) (*client.PlanResponse, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, benchmarkPlanQualityGateWait)
	defer cancel()
	ticker := time.NewTicker(benchmarkPlanQualityGatePollInterval)
	defer ticker.Stop()
	for {
		planResp, err := c.GetObjectivePlan(deadlineCtx, objectiveID)
		if err == nil && planResp != nil && planResp.Plan.ID != "" {
			return planResp, nil
		}
		select {
		case <-deadlineCtx.Done():
			return nil, fmt.Errorf("waiting for benchmark plan: %w", deadlineCtx.Err())
		case <-ticker.C:
		}
	}
}

func enforceBenchmarkPlanShape(spec benchmark.Spec, planResp *client.PlanResponse) error {
	if planResp == nil {
		return fmt.Errorf("benchmark plan missing")
	}
	if err := spec.ValidatePlanShape(planResp.Streams); err != nil {
		return fmt.Errorf("benchmark plan shape mismatch: %w", err)
	}
	return nil
}

func defaultBenchmarkReportPath(run benchmark.Run) (string, error) {
	root, err := currentProjectRoot()
	if err != nil {
		return "", err
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	createdAt := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, run.CreatedAt); err == nil {
		createdAt = parsed.UTC()
	}
	name := strings.ReplaceAll(run.BenchmarkID, ".", "-")
	fileName := fmt.Sprintf("%s-%s-%s.md", createdAt.Format("2006-01-02"), name, run.ID)
	return filepath.Join(root, "docs", "benchmarks", fileName), nil
}

func writeBenchmarkReport(reportPath, content string) error {
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return fmt.Errorf("create benchmark report directory: %w", err)
	}
	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write benchmark report: %w", err)
	}
	return nil
}

var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Manage Tack benchmarks",
}

var benchmarkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List built-in benchmarks",
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, spec := range benchmark.BuiltInSpecs() {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", spec.ID, spec.DisplayName, spec.Family, strings.Join(spec.Tags, ",")); err != nil {
				return err
			}
		}
		return nil
	},
}

var benchmarkShowCmd = &cobra.Command{
	Use:   "show [id]",
	Short: "Show a benchmark spec",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, ok := benchmark.FindSpec(args[0])
		if !ok {
			return fmt.Errorf("unknown benchmark %q", args[0])
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "ID: %s\nName: %s\nRepo: %s\nRepo URL: %s\nDefault branch: %s\nFamily: %s\nTags: %s\nReadiness: %s\nBaseline: %s\nFeature range: %s\nContext policy: %s\nValidation: %s\nPrompt: %s\nOutstanding: %s\n", spec.ID, spec.DisplayName, spec.Repo, spec.RepoURL, spec.DefaultBranch, spec.Family, strings.Join(spec.Tags, ", "), spec.Readiness(), spec.Baseline, spec.FeatureRange, spec.ContextPolicy, spec.Validation, spec.Prompt, spec.OutstandingText()); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkRunbookCmd = &cobra.Command{
	Use:   "runbook [id]",
	Short: "Show the manual preparation runbook for a benchmark",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, ok := benchmark.FindSpec(args[0])
		if !ok {
			return fmt.Errorf("unknown benchmark %q", args[0])
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Benchmark: %s\nReadiness: %s\nStep 1: clone %s into a clean benchmark workspace\nStep 2: check out baseline %s\nStep 3: confirm the workspace HEAD matches the frozen feature pre-state from %s\nStep 4: register the repo with Tack and capture planning artifacts\nStep 5: validate with %s\n", spec.ID, spec.Readiness(), spec.RepoURL, spec.Baseline, spec.DefaultBranch, spec.Validation); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkReadyCmd = &cobra.Command{
	Use:   "ready [id]",
	Short: "Show benchmark readiness and outstanding requirements",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, ok := benchmark.FindSpec(args[0])
		if !ok {
			return fmt.Errorf("unknown benchmark %q", args[0])
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Benchmark: %s\nReadiness: %s\nOutstanding: %s\n", spec.ID, spec.Readiness(), spec.OutstandingText()); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkPrepareCmd = &cobra.Command{
	Use:   "prepare [id]",
	Short: "Prepare a benchmark workspace at the frozen baseline",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		spec, ok := benchmark.FindSpec(args[0])
		if !ok {
			return fmt.Errorf("unknown benchmark %q", args[0])
		}
		workspace := benchmarkPrepareWorkspace
		if strings.TrimSpace(workspace) == "" {
			workspace = filepath.Join(cfg.Daemon.DataDir, "benchmarks", "workspaces", strings.ReplaceAll(spec.ID, ".", "-"))
		}
		prep, err := benchmark.PrepareWorkspace(spec, benchmark.PrepareOptions{
			Source:    benchmarkPrepareSource,
			Workspace: workspace,
			Baseline:  benchmarkPrepareBaseline,
		})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Prepared benchmark workspace\nBenchmark: %s\nWorkspace: %s\nSource: %s\nBaseline: %s\nHEAD: %s\nPreflight: passed (%d gates)\n", prep.BenchmarkID, prep.Workspace, prep.Source, prep.Baseline, prep.Head, len(spec.QualityGates)); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkRunCmd = &cobra.Command{
	Use:   "run [id]",
	Short: "Prepare a benchmark run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		run, ok := benchmark.PrepareRun(args[0], benchmarkRunMode, benchmarkRunBlueprint)
		if !ok {
			return fmt.Errorf("unknown benchmark %q", args[0])
		}
		if err := benchmark.SaveRun(cfg.Daemon.DataDir, run); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Created benchmark run %s\nBenchmark: %s\nStatus: %s\nMode: %s\nBlueprint: %s\n", run.ID, run.BenchmarkID, run.Status, run.EffectiveMode, run.Blueprint); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkExecuteCmd = &cobra.Command{
	Use:   "execute [run-id]",
	Short: "Start a real benchmark run against a prepared workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		run, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[0])
		}
		spec, ok := benchmark.FindSpec(run.BenchmarkID)
		if !ok {
			return fmt.Errorf("unknown benchmark %q", run.BenchmarkID)
		}
		workspace := benchmarkExecuteWorkspace
		if strings.TrimSpace(workspace) == "" {
			workspace = filepath.Join(cfg.Daemon.DataDir, "benchmarks", "workspaces", strings.ReplaceAll(spec.ID, ".", "-"))
		}
		passed, checkErr := benchmark.HasPassingPreflight(spec, workspace, run.Baseline, "")
		if checkErr != nil {
			return checkErr
		}
		if !passed {
			return fmt.Errorf("workspace %q has missing, stale, dirty, or failing benchmark preflight; rerun `tack benchmark prepare %s`", workspace, spec.ID)
		}
		c, err := newDaemonClient(cmd, false)
		if err != nil {
			return err
		}
		project, err := c.ResolveProjectByPath(cmd.Context(), workspace)
		if err != nil {
			projectUUID, idErr := ensureStableProjectID(workspace)
			if idErr != nil {
				return idErr
			}
			project, err = c.RegisterProject(cmd.Context(), client.ProjectRegistration{
				ProjectID:  projectUUID,
				RootPath:   workspace,
				ConfigPath: config.ProjectConfigPath(workspace),
			})
			if err != nil {
				return err
			}
		}
		c.SetProjectID(project.ID)
		obj, err := c.CreateObjectiveWithOptions(cmd.Context(), run.PromptSnapshot, client.CreateObjectiveOptions{Blueprint: run.Blueprint})
		if err != nil {
			return err
		}
		if err := applyBenchmarkPlanQualityGates(cmd.Context(), c, obj.ID, spec.QualityGates); err != nil {
			return err
		}
		planResp, err := waitForBenchmarkPlan(cmd.Context(), c, obj.ID)
		if err != nil {
			return err
		}
		if err := enforceBenchmarkPlanShape(spec, planResp); err != nil {
			return err
		}
		snap, err := c.ObjectiveRunSnapshot(cmd.Context(), obj.ID)
		if err == nil {
			run.RunID = snap.RunID
			run.Status = benchmarkStatusFromRunSnapshot(snap.Status)
		}
		run.Workspace = workspace
		run.ProjectID = project.ID
		run.ObjectiveID = obj.ID
		if run.Status == "planned" || run.Status == "" {
			run.Status = string(obj.Status)
		}
		if err := benchmark.UpdateRun(cfg.Daemon.DataDir, run); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Started benchmark execution\nRun: %s\nProject: %s\nObjective: %s\nWorkspace: %s\nStatus: %s\n", run.ID, run.ProjectID, run.ObjectiveID, run.Workspace, run.Status); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List recorded benchmark runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		runs, err := benchmark.LoadRuns(cfg.Daemon.DataDir)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%s\n", run.ID, run.BenchmarkID, run.Status, run.EffectiveMode, run.Blueprint, run.ScoreStatus); err != nil {
				return err
			}
		}
		return nil
	},
}

var benchmarkShowRunCmd = &cobra.Command{
	Use:   "show-run [id]",
	Short: "Show a recorded benchmark run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		run, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[0])
		}
		_ = syncBenchmarkRun(cmd, cfg, &run)
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Run: %s\nDaemon run: %s\nBenchmark: %s\nStatus: %s\nReason: %s\nRepo: %s\nFamily: %s\nReadiness snapshot: %s\nBaseline: %s\nContext policy: %s\nRequested mode: %s\nEffective mode: %s\nBlueprint: %s\nWorkspace: %s\nProject: %s\nObjective: %s\nVersion: %s\nCreated: %s\nPrompt snapshot: %s\nScore: %s\n", run.ID, run.RunID, run.BenchmarkID, run.Status, run.StatusReason, run.Repo, run.Family, run.ReadinessSnapshot, run.Baseline, run.ContextPolicy, run.RequestedMode, run.EffectiveMode, run.Blueprint, run.Workspace, run.ProjectID, run.ObjectiveID, run.Version, run.CreatedAt, run.PromptSnapshot, run.ScoreStatus); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkReportCmd = &cobra.Command{
	Use:   "report [id]",
	Short: "Generate benchmark telemetry report for a run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		run, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[0])
		}
		_ = syncBenchmarkRun(cmd, cfg, &run)
		report, err := benchmark.BuildReport(cfg.Daemon.DataDir, run)
		if err != nil {
			return err
		}
		markdown := benchmark.RenderReportMarkdown(report)
		reportPath := strings.TrimSpace(benchmarkReportOut)
		if reportPath == "" && benchmarkReportWrite {
			reportPath, err = defaultBenchmarkReportPath(run)
			if err != nil {
				return err
			}
		}
		if reportPath != "" {
			if !filepath.IsAbs(reportPath) {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				reportPath = filepath.Join(cwd, reportPath)
			}
			if err := writeBenchmarkReport(reportPath, markdown); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Wrote benchmark report to %s\n", reportPath)
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), markdown)
		return err
	},
}

var benchmarkSupersedeCmd = &cobra.Command{
	Use:   "supersede [run-id]",
	Short: "Mark a benchmark run superseded by a newer design or rerun",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		run, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[0])
		}
		run.Status = "superseded"
		run.StatusReason = benchmarkSupersedeReason
		if run.StatusReason == "" {
			run.StatusReason = "superseded by newer benchmark design/run"
		}
		if err := benchmark.UpdateRun(cfg.Daemon.DataDir, run); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Superseded benchmark run %s\nReason: %s\n", run.ID, run.StatusReason); err != nil {
			return err
		}
		return nil
	},
}

var benchmarkCompareCmd = &cobra.Command{
	Use:   "compare [run-a] [run-b]",
	Short: "Compare two recorded benchmark runs",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadUserConfigOnly()
		if err != nil {
			return err
		}
		runA, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[0])
		}
		runB, ok, err := benchmark.FindRun(cfg.Daemon.DataDir, args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("unknown benchmark run %q", args[1])
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "A: %s\nB: %s\nBenchmark: %s\nReadiness: %s -> %s\nStatus: %s -> %s\nMode: %s -> %s\nBlueprint: %s -> %s\nVersion: %s -> %s\nScore: %s -> %s\n", runA.ID, runB.ID, runA.BenchmarkID, runA.ReadinessSnapshot, runB.ReadinessSnapshot, runA.Status, runB.Status, runA.EffectiveMode, runB.EffectiveMode, runA.Blueprint, runB.Blueprint, runA.Version, runB.Version, runA.ScoreStatus, runB.ScoreStatus); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	benchmarkRunCmd.Flags().StringVar(&benchmarkRunMode, "mode", "", "requested benchmark run mode override")
	benchmarkRunCmd.Flags().StringVar(&benchmarkRunBlueprint, "blueprint", "", "benchmark blueprint override")
	benchmarkPrepareCmd.Flags().StringVar(&benchmarkPrepareSource, "source", "", "source repo path or URL override")
	benchmarkPrepareCmd.Flags().StringVar(&benchmarkPrepareWorkspace, "workspace", "", "workspace directory for prepared benchmark repo")
	benchmarkPrepareCmd.Flags().StringVar(&benchmarkPrepareBaseline, "baseline", "", "baseline commit override")
	benchmarkExecuteCmd.Flags().StringVar(&benchmarkExecuteWorkspace, "workspace", "", "prepared workspace directory override")
	benchmarkSupersedeCmd.Flags().StringVar(&benchmarkSupersedeReason, "reason", "", "reason for superseding the benchmark run")
	benchmarkReportCmd.Flags().BoolVar(&benchmarkReportWrite, "write", false, "write benchmark report markdown to docs/benchmarks by default")
	benchmarkReportCmd.Flags().StringVar(&benchmarkReportOut, "out", "", "write benchmark report markdown to a specific path")
	benchmarkCmd.AddCommand(benchmarkListCmd)
	benchmarkCmd.AddCommand(benchmarkShowCmd)
	benchmarkCmd.AddCommand(benchmarkReadyCmd)
	benchmarkCmd.AddCommand(benchmarkRunbookCmd)
	benchmarkCmd.AddCommand(benchmarkPrepareCmd)
	benchmarkCmd.AddCommand(benchmarkRunCmd)
	benchmarkCmd.AddCommand(benchmarkExecuteCmd)
	benchmarkCmd.AddCommand(benchmarkSupersedeCmd)
	benchmarkCmd.AddCommand(benchmarkRunsCmd)
	benchmarkCmd.AddCommand(benchmarkShowRunCmd)
	benchmarkCmd.AddCommand(benchmarkReportCmd)
	benchmarkCmd.AddCommand(benchmarkCompareCmd)
	rootCmd.AddCommand(benchmarkCmd)
}
