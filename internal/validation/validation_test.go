package validation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/runtimecatalog"
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

func TestGlobalSetupFindingsReportIncompletePhase(t *testing.T) {
	findings := GlobalSetupFindings(config.SetupConfig{Phases: map[string]config.SetupPhase{"daemon_service": {Complete: true}}}, []string{"daemon_service", "runtime", "final_validation"})
	if len(findings) != 1 || findings[0].Status != StatusFail || !strings.Contains(findings[0].Evidence, "next_phase=runtime") || findings[0].Fix == "" {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestEffectiveConfigFindingsIncludeSourceEvidenceAndFix(t *testing.T) {
	effective := &config.EffectiveConfig{
		Runtime:      config.EffectiveString{Value: "pi", Source: config.ValueSourceGlobal, Set: true},
		Provider:     config.EffectiveString{Source: config.ValueSourceMissing},
		QualityGates: config.EffectiveStringSlice{Value: []string{"go test ./..."}, Source: config.ValueSourceProject, Set: true},
	}
	findings := EffectiveConfigFindings(effective)
	if findings[0].Status != StatusPass || findings[0].Source != "global" || findings[0].Evidence != "agents.runtime=pi" {
		t.Fatalf("runtime finding = %#v", findings[0])
	}
	if findings[1].Status != StatusFail || findings[1].Source != "missing" || !strings.Contains(findings[1].Evidence, "runtime_auth.provider") || findings[1].Fix == "" {
		t.Fatalf("provider finding = %#v", findings[1])
	}
	if findings[len(findings)-1].Status != StatusPass || findings[len(findings)-1].Source != "project_override" {
		t.Fatalf("quality gate finding = %#v", findings[len(findings)-1])
	}
}

func TestPiModelCatalogFindingsValidateProviderAndModels(t *testing.T) {
	selection := PiModelSelection{
		Runtime:        config.EffectiveString{Value: "pi", Source: config.ValueSourceGlobal, Set: true},
		Provider:       config.EffectiveString{Value: "anthropic", Source: config.ValueSourceGlobal, Set: true},
		PlannerModel:   config.EffectiveString{Value: "planner", Source: config.ValueSourceGlobal, Set: true},
		AgentModel:     config.EffectiveString{Value: "missing", Source: config.ValueSourceProject, Set: true},
		SmallTaskModel: config.EffectiveString{Value: "small", Source: config.ValueSourceGlobal, Set: true},
	}
	probe := runtimecatalog.PiProbe{Installed: true, Catalog: []runtimecatalog.ProviderCatalog{{Provider: "anthropic", Models: []runtimecatalog.Model{{ID: "planner"}, {ID: "small"}}}}}
	findings := PiModelCatalogFindings(selection, probe)
	if len(findings) != 1 || findings[0].Status != StatusFail || findings[0].Source != "project_override" || !strings.Contains(findings[0].Evidence, "models.agent=missing") || findings[0].Fix == "" {
		t.Fatalf("findings = %#v", findings)
	}
}
