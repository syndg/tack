package validation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunAggregatesFindingsAndErrors(t *testing.T) {
	report := Run(context.Background(),
		Check{Name: "config", Run: func(context.Context) ([]Finding, error) {
			return []Finding{{Status: StatusPass, Source: "global", Evidence: "loaded"}}, nil
		}},
		Check{Name: "runtime", Run: func(context.Context) ([]Finding, error) {
			return nil, errors.New("pi not found")
		}},
	)

	if len(report.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(report.Findings))
	}
	if !report.Failed {
		t.Fatal("report.Failed = false, want true")
	}
	if report.Findings[0].Check != "config" || report.Findings[1].Check != "runtime" {
		t.Fatalf("checks = %#v", report.Findings)
	}
}

func TestRenderHumanIncludesActionableFields(t *testing.T) {
	report := Report{Findings: []Finding{{Check: "auth", Status: StatusFail, Source: "project", Evidence: "missing openai", Severity: SeverityError, Fix: "tack auth add openai"}}, Failed: true}
	var out bytes.Buffer
	if err := RenderHuman(&out, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	text := out.String()
	for _, want := range []string{"[fail] auth", "source: project", "evidence: missing openai", "severity: error", "fix: tack auth add openai"} {
		if !strings.Contains(text, want) {
			t.Fatalf("human output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderJSONStableShape(t *testing.T) {
	report := Report{Findings: []Finding{{Check: "daemon", Status: StatusWarn, Source: "global", Evidence: "not running", Fix: "tack daemon"}}}
	var out bytes.Buffer
	if err := RenderJSON(&out, report); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal JSON: %v\n%s", err, out.String())
	}
	if decoded.Findings[0].Severity != SeverityWarn || decoded.Findings[0].Status != StatusWarn {
		t.Fatalf("decoded = %#v", decoded.Findings[0])
	}
}

func TestFailureSeverityBehavior(t *testing.T) {
	report := Run(context.Background(), Check{Name: "optional", Run: func(context.Context) ([]Finding, error) {
		return []Finding{{Status: StatusFail, Severity: SeverityWarn, Evidence: "optional missing"}}, nil
	}})
	if report.Failed {
		t.Fatal("warning-severity failure should not fail report")
	}
}
