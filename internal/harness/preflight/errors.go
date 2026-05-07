package preflight

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/validation"
)

type Problem struct {
	Requirement string
	Summary     string
	Evidence    string
	Fix         string
}

type Failure struct {
	BlueprintID string
	Problems    []Problem
}

func (f Failure) Error() string {
	var b strings.Builder
	if f.BlueprintID != "" {
		b.WriteString(fmt.Sprintf("preflight failed for blueprint %q:", f.BlueprintID))
	} else {
		b.WriteString("preflight failed:")
	}
	for _, p := range f.Problems {
		b.WriteString("\n- ")
		if p.Requirement != "" {
			b.WriteString(p.Requirement)
			b.WriteString(": ")
		}
		b.WriteString(p.Summary)
		if p.Evidence != "" {
			b.WriteString("\n  evidence: ")
			b.WriteString(p.Evidence)
		}
		if p.Fix != "" {
			b.WriteString("\n  fix: ")
			b.WriteString(p.Fix)
		}
	}
	return b.String()
}

func (f Failure) Is(target error) bool {
	_, ok := target.(Failure)
	return ok
}

func (f Failure) Findings() []validation.Finding {
	findings := make([]validation.Finding, 0, len(f.Problems))
	for _, problem := range f.Problems {
		check := "preflight"
		if problem.Requirement != "" {
			check += "." + problem.Requirement
		}
		details := map[string]string{}
		if f.BlueprintID != "" {
			details["blueprint_id"] = f.BlueprintID
		}
		if problem.Requirement != "" {
			details["requirement"] = problem.Requirement
		}
		findings = append(findings, validation.Finding{
			Check:    check,
			Status:   validation.StatusFail,
			Summary:  problem.Summary,
			Source:   "blueprint_requirement",
			Evidence: problem.Evidence,
			Severity: validation.SeverityError,
			Fix:      problem.Fix,
			Details:  details,
		})
	}
	return findings
}

func (f Failure) Report() validation.Report {
	return validation.Report{Findings: f.Findings(), Failed: len(f.Problems) > 0}
}
