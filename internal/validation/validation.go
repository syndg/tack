package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

type Finding struct {
	Check    string            `json:"check"`
	Status   Status            `json:"status"`
	Summary  string            `json:"summary,omitempty"`
	Source   string            `json:"source,omitempty"`
	Evidence string            `json:"evidence,omitempty"`
	Severity Severity          `json:"severity"`
	Fix      string            `json:"fix,omitempty"`
	Details  map[string]string `json:"details,omitempty"`
}

type Check struct {
	Name string
	Run  func(context.Context) ([]Finding, error)
}

type Report struct {
	Findings []Finding `json:"findings"`
	Failed   bool      `json:"failed"`
}

func Run(ctx context.Context, checks ...Check) Report {
	var report Report
	for _, check := range checks {
		if check.Run == nil {
			continue
		}
		findings, err := check.Run(ctx)
		if err != nil {
			findings = append(findings, Finding{
				Check:    check.Name,
				Status:   StatusFail,
				Severity: SeverityError,
				Evidence: err.Error(),
			})
		}
		for _, finding := range findings {
			if finding.Check == "" {
				finding.Check = check.Name
			}
			if finding.Severity == "" {
				finding.Severity = severityForStatus(finding.Status)
			}
			if finding.Status == StatusFail && finding.Severity == SeverityError {
				report.Failed = true
			}
			report.Findings = append(report.Findings, finding)
		}
	}
	return report
}

func RenderHuman(w io.Writer, report Report) error {
	report = normalizeReport(report)
	for _, finding := range report.Findings {
		if _, err := fmt.Fprintf(w, "[%s] %s", finding.Status, finding.Check); err != nil {
			return err
		}
		if finding.Summary != "" {
			if _, err := fmt.Fprintf(w, ": %s", finding.Summary); err != nil {
				return err
			}
		}
		parts := []string{}
		if finding.Source != "" {
			parts = append(parts, "source: "+finding.Source)
		}
		if finding.Evidence != "" {
			parts = append(parts, "evidence: "+finding.Evidence)
		}
		if finding.Severity != "" {
			parts = append(parts, "severity: "+string(finding.Severity))
		}
		if len(parts) > 0 {
			if _, err := fmt.Fprintf(w, " (%s)", strings.Join(parts, "; ")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if finding.Fix != "" {
			if _, err := fmt.Fprintf(w, "  fix: %s\n", finding.Fix); err != nil {
				return err
			}
		}
	}
	return nil
}

func RenderJSON(w io.Writer, report Report) error {
	report = normalizeReport(report)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func normalizeReport(report Report) Report {
	for i := range report.Findings {
		if report.Findings[i].Severity == "" {
			report.Findings[i].Severity = severityForStatus(report.Findings[i].Status)
		}
		if report.Findings[i].Status == StatusFail && report.Findings[i].Severity == SeverityError {
			report.Failed = true
		}
	}
	return report
}

func severityForStatus(status Status) Severity {
	switch status {
	case StatusFail:
		return SeverityError
	case StatusWarn:
		return SeverityWarn
	default:
		return SeverityInfo
	}
}
