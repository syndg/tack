package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type PrepareOptions struct {
	Source    string
	Workspace string
	Baseline  string
}

type PreparedWorkspace struct {
	BenchmarkID string
	Source      string
	Workspace   string
	Baseline    string
	Head        string
	Preflight   *PreflightReport
}

type PreflightReport struct {
	BenchmarkID  string   `json:"benchmark_id"`
	Workspace    string   `json:"workspace"`
	Baseline     string   `json:"baseline"`
	Head         string   `json:"head"`
	QualityGates []string `json:"quality_gates"`
	Clean        bool     `json:"clean"`
	Passed       bool     `json:"passed"`
	ValidatedAt  string   `json:"validated_at"`
}

func preflightReportPath(workspace string) string {
	return filepath.Join(workspace, ".tack", "benchmark-preflight.json")
}

func workspaceHead(workspace string) (string, error) {
	headOut, err := exec.Command("git", "-C", workspace, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, strings.TrimSpace(string(headOut)))
	}
	return strings.TrimSpace(string(headOut)), nil
}

func writePreflightReport(report PreflightReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal benchmark preflight: %w", err)
	}
	if err := os.WriteFile(preflightReportPath(report.Workspace), raw, 0o644); err != nil {
		return fmt.Errorf("write benchmark preflight: %w", err)
	}
	return nil
}

func workspaceClean(workspace string) (bool, error) {
	out, err := exec.Command("git", "-C", workspace, "status", "--short").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("git status --short: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path := line
		if len(path) > 3 {
			path = strings.TrimSpace(path[3:])
		}
		if strings.HasPrefix(path, ".tack/") || path == ".tack" {
			continue
		}
		return false, nil
	}
	return true, nil
}

func preflightConfigContents(spec Spec) string {
	var b strings.Builder
	b.WriteString("daemon:\n  base_branch: benchmark-base\n")
	if len(spec.QualityGates) > 0 {
		b.WriteString("quality_gates:\n")
		for _, gate := range spec.QualityGates {
			b.WriteString("  - ")
			b.WriteString(gate)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func runPreflight(spec Spec, workspace, baseline, head string) (*PreflightReport, error) {
	report := &PreflightReport{
		BenchmarkID:  spec.ID,
		Workspace:    workspace,
		Baseline:     baseline,
		Head:         head,
		QualityGates: append([]string(nil), spec.QualityGates...),
		ValidatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if len(spec.QualityGates) == 0 {
		clean, err := workspaceClean(workspace)
		if err != nil {
			return nil, err
		}
		report.Clean = clean
		if !clean {
			_ = writePreflightReport(*report)
			return nil, fmt.Errorf("benchmark preflight left workspace dirty for %q", spec.ID)
		}
		report.Passed = true
		if err := writePreflightReport(*report); err != nil {
			return nil, err
		}
		return report, nil
	}
	for i, gate := range spec.QualityGates {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		cmd := exec.CommandContext(ctx, "sh", "-c", gate)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			_ = writePreflightReport(*report)
			trimmed := strings.TrimSpace(string(out))
			if trimmed == "" {
				trimmed = err.Error()
			}
			return nil, fmt.Errorf("benchmark preflight failed for %q at gate %d (%s): %s", spec.ID, i+1, gate, trimmed)
		}
	}
	clean, err := workspaceClean(workspace)
	if err != nil {
		return nil, err
	}
	report.Clean = clean
	if !clean {
		_ = writePreflightReport(*report)
		return nil, fmt.Errorf("benchmark preflight left workspace dirty for %q", spec.ID)
	}
	report.Passed = true
	if err := writePreflightReport(*report); err != nil {
		return nil, err
	}
	return report, nil
}

func LoadPreflightReport(workspace string) (PreflightReport, bool, error) {
	raw, err := os.ReadFile(preflightReportPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return PreflightReport{}, false, nil
		}
		return PreflightReport{}, false, fmt.Errorf("read benchmark preflight: %w", err)
	}
	var report PreflightReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return PreflightReport{}, false, fmt.Errorf("decode benchmark preflight: %w", err)
	}
	return report, true, nil
}

func HasPassingPreflight(spec Spec, workspace, baseline, head string) (bool, error) {
	report, ok, err := LoadPreflightReport(workspace)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if strings.TrimSpace(head) == "" {
		head, err = workspaceHead(workspace)
		if err != nil {
			return false, err
		}
	}
	if !report.Passed || report.BenchmarkID != spec.ID || report.Baseline != baseline || report.Head != head {
		return false, nil
	}
	clean, err := workspaceClean(workspace)
	if err != nil {
		return false, err
	}
	if !report.Clean || !clean {
		return false, nil
	}
	if !slices.Equal(report.QualityGates, spec.QualityGates) {
		return false, nil
	}
	return true, nil
}

func PrepareWorkspace(spec Spec, opts PrepareOptions) (PreparedWorkspace, error) {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = spec.RepoURL
	}
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		return PreparedWorkspace{}, fmt.Errorf("workspace is required")
	}
	baseline := strings.TrimSpace(opts.Baseline)
	if baseline == "" {
		baseline = spec.Baseline
	}
	if baseline == "" || baseline == "pending-selection" {
		return PreparedWorkspace{}, fmt.Errorf("benchmark %q does not have a frozen baseline yet", spec.ID)
	}
	if _, err := os.Stat(workspace); err == nil {
		entries, readErr := os.ReadDir(workspace)
		if readErr != nil {
			return PreparedWorkspace{}, fmt.Errorf("read workspace: %w", readErr)
		}
		if len(entries) > 0 {
			return PreparedWorkspace{}, fmt.Errorf("workspace %q already exists and is not empty", workspace)
		}
	} else if !os.IsNotExist(err) {
		return PreparedWorkspace{}, fmt.Errorf("stat workspace: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(workspace), 0o755); err != nil {
		return PreparedWorkspace{}, fmt.Errorf("mkdir workspace parent: %w", err)
	}
	if out, err := exec.Command("git", "clone", source, workspace).CombinedOutput(); err != nil {
		return PreparedWorkspace{}, fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if spec.RepoURL != "" {
		if info, statErr := os.Stat(source); statErr == nil && info.IsDir() {
			if out, err := exec.Command("git", "-C", workspace, "remote", "set-url", "origin", spec.RepoURL).CombinedOutput(); err != nil {
				return PreparedWorkspace{}, fmt.Errorf("git remote set-url origin: %w: %s", err, strings.TrimSpace(string(out)))
			}
		}
	}
	if out, err := exec.Command("git", "-C", workspace, "checkout", baseline).CombinedOutput(); err != nil {
		return PreparedWorkspace{}, fmt.Errorf("git checkout baseline: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", workspace, "branch", "-f", "benchmark-base", baseline).CombinedOutput(); err != nil {
		return PreparedWorkspace{}, fmt.Errorf("git create benchmark base branch: %w: %s", err, strings.TrimSpace(string(out)))
	}
	tackDir := filepath.Join(workspace, ".tack")
	if err := os.MkdirAll(tackDir, 0o755); err != nil {
		return PreparedWorkspace{}, fmt.Errorf("mkdir .tack: %w", err)
	}
	configPath := filepath.Join(tackDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configContents := preflightConfigContents(spec)
		if writeErr := os.WriteFile(configPath, []byte(configContents), 0o644); writeErr != nil {
			return PreparedWorkspace{}, fmt.Errorf("write benchmark project config: %w", writeErr)
		}
	} else if err != nil {
		return PreparedWorkspace{}, fmt.Errorf("stat benchmark project config: %w", err)
	}
	head, err := workspaceHead(workspace)
	if err != nil {
		return PreparedWorkspace{}, err
	}
	preflight, err := runPreflight(spec, workspace, baseline, head)
	if err != nil {
		return PreparedWorkspace{}, err
	}
	return PreparedWorkspace{
		BenchmarkID: spec.ID,
		Source:      source,
		Workspace:   workspace,
		Baseline:    baseline,
		Head:        head,
		Preflight:   preflight,
	}, nil
}
