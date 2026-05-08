package validation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/runtimecatalog"
)

type EffectiveStringField struct {
	Name  string
	Value config.EffectiveString
}

type EffectiveStringSliceField struct {
	Name  string
	Value config.EffectiveStringSlice
}

func GlobalSetupFindings(setup config.SetupConfig, phaseOrder []string) []Finding {
	if setup.Complete {
		if strings.TrimSpace(setup.Service) == "" {
			return []Finding{{
				Status:   StatusFail,
				Source:   "global",
				Evidence: "setup.complete=true setup.service is not set",
				Fix:      "run tack setup to record the daemon service selection",
			}}
		}
		return []Finding{{Status: StatusPass, Source: "global", Evidence: "setup.complete=true setup.service=" + setup.Service}}
	}
	evidence := "setup.complete=false"
	if phase := FirstIncompleteSetupPhase(setup, phaseOrder); phase != "" {
		evidence += " next_phase=" + phase
	}
	return []Finding{{
		Status:   StatusFail,
		Source:   "global",
		Evidence: evidence,
		Fix:      "run tack setup to completion",
	}}
}

func FirstIncompleteSetupPhase(setup config.SetupConfig, phaseOrder []string) string {
	if setup.Complete {
		return ""
	}
	for _, phase := range phaseOrder {
		if setup.Phases == nil || !setup.Phases[phase].Complete {
			return phase
		}
	}
	if len(phaseOrder) > 0 {
		return phaseOrder[len(phaseOrder)-1]
	}
	return ""
}

func EffectiveConfigFindings(effective *config.EffectiveConfig) []Finding {
	findings := []Finding{}
	for _, field := range EffectiveConfigStringFields(effective) {
		findings = append(findings, EffectiveStringFinding(field.Name, field.Value))
	}
	for _, field := range EffectiveConfigStringSliceFields(effective) {
		findings = append(findings, EffectiveStringSliceFinding(field.Name, field.Value))
	}
	return findings
}

func EffectiveConfigStringFields(effective *config.EffectiveConfig) []EffectiveStringField {
	return []EffectiveStringField{
		{Name: "agents.runtime", Value: effective.Runtime},
		{Name: "runtime_auth.provider", Value: effective.Provider},
		{Name: "runtime_auth.mode", Value: effective.AuthMode},
		{Name: "runtime_auth.method", Value: effective.AuthMethod},
		{Name: "runtime_auth.credential_ref", Value: effective.CredentialRef},
		{Name: "models.planner", Value: effective.PlannerModel},
		{Name: "models.agent", Value: effective.AgentModel},
		{Name: "models.small_tasks", Value: effective.SmallTaskModel},
		{Name: "sandbox.provider", Value: effective.SandboxProvider},
		{Name: "blueprint", Value: effective.Blueprint},
	}
}

func EffectiveConfigStringSliceFields(effective *config.EffectiveConfig) []EffectiveStringSliceField {
	return []EffectiveStringSliceField{{Name: "quality_gates", Value: effective.QualityGates}}
}

func EffectiveStringFinding(field string, value config.EffectiveString) Finding {
	if !value.Set {
		return Finding{
			Status:   StatusFail,
			Source:   string(config.ValueSourceMissing),
			Evidence: field + " is not set by global setup or project config",
			Fix:      "run tack setup or set an explicit project override with tack init",
		}
	}
	return Finding{
		Status:   StatusPass,
		Source:   string(value.Source),
		Evidence: fmt.Sprintf("%s=%s", field, value.Value),
	}
}

func EffectiveStringSliceFinding(field string, value config.EffectiveStringSlice) Finding {
	if !value.Set || len(value.Value) == 0 {
		return Finding{
			Status:   StatusFail,
			Source:   string(config.ValueSourceMissing),
			Evidence: field + " is not set by global setup or project config",
			Fix:      "run tack setup or set an explicit project override with tack init",
		}
	}
	return Finding{
		Status:   StatusPass,
		Source:   string(value.Source),
		Evidence: fmt.Sprintf("%s=%s", field, strings.Join(value.Value, "; ")),
	}
}

func PIRuntimeFindings(ctx context.Context, runner runtimecatalog.Runner, runtime string) ([]Finding, runtimecatalog.PiProbe) {
	if runtime != "pi" {
		return []Finding{{Status: StatusSkip, Source: "effective_config", Evidence: "agents.runtime=" + runtime}}, runtimecatalog.PiProbe{}
	}
	probe := runtimecatalog.ProbePi(ctx, runner)
	if !probe.Installed {
		fix := "install Pi or select a different runtime"
		if len(probe.PackageManagers) == 0 {
			fix = "install Node.js/npm or Bun before installing Pi, or select a different runtime"
		}
		return []Finding{{
			Status:   StatusFail,
			Source:   "environment",
			Evidence: "pi executable was not found in PATH",
			Fix:      fix,
			Details: map[string]string{
				"package_managers": strings.Join(probe.PackageManagers, ","),
			},
		}}, probe
	}
	if probe.CatalogError != "" {
		return []Finding{{
			Status:   StatusFail,
			Source:   "pi",
			Evidence: probe.CatalogError,
			Fix:      "repair Pi installation or choose models after pi --list-models succeeds",
		}}, probe
	}
	models := 0
	for _, provider := range probe.Catalog {
		models += len(provider.Models)
	}
	return []Finding{{
		Status:   StatusPass,
		Source:   "environment",
		Evidence: fmt.Sprintf("pi=%s providers=%d models=%d", probe.Path, len(probe.Catalog), models),
	}}, probe
}

type PiModelSelection struct {
	Runtime        config.EffectiveString
	Provider       config.EffectiveString
	PlannerModel   config.EffectiveString
	AgentModel     config.EffectiveString
	SmallTaskModel config.EffectiveString
}

func PiModelCatalogFindings(selection PiModelSelection, probe runtimecatalog.PiProbe) []Finding {
	if !selection.Runtime.Set || selection.Runtime.Value != "pi" {
		evidence := "agents.runtime is not set"
		if selection.Runtime.Set {
			evidence = "agents.runtime=" + selection.Runtime.Value
		}
		return []Finding{{Status: StatusSkip, Source: string(selection.Runtime.Source), Evidence: evidence}}
	}
	missing := MissingPiModelCatalogInputs(selection)
	if len(missing) > 0 {
		return []Finding{{
			Status:   StatusSkip,
			Source:   string(config.ValueSourceMissing),
			Evidence: "missing " + strings.Join(missing, ", "),
			Fix:      "run tack setup or set explicit project provider and model overrides with tack init",
		}}
	}
	if !probe.Installed {
		return []Finding{{
			Status:   StatusSkip,
			Source:   "environment",
			Evidence: "pi executable was not found in PATH",
			Fix:      "install Pi before validating Pi model selections",
		}}
	}
	if probe.CatalogError != "" {
		return []Finding{{
			Status:   StatusFail,
			Source:   "pi",
			Evidence: probe.CatalogError,
			Fix:      "repair Pi installation or choose models after pi --list-models succeeds",
		}}
	}
	if !runtimecatalog.CatalogHasProvider(probe.Catalog, selection.Provider.Value) {
		available := strings.Join(runtimecatalog.CatalogProviderNames(probe.Catalog), ", ")
		if available == "" {
			available = "none"
		}
		return []Finding{{
			Status:   StatusFail,
			Source:   string(selection.Provider.Source),
			Evidence: fmt.Sprintf("provider=%s available_providers=%s", selection.Provider.Value, available),
			Fix:      "select a provider from the Pi model catalog",
		}}
	}
	modelFields := []struct {
		name  string
		value config.EffectiveString
	}{
		{name: "models.planner", value: selection.PlannerModel},
		{name: "models.agent", value: selection.AgentModel},
		{name: "models.small_tasks", value: selection.SmallTaskModel},
	}
	for _, field := range modelFields {
		if !runtimecatalog.CatalogHasModel(probe.Catalog, selection.Provider.Value, field.value.Value) {
			available := strings.Join(runtimecatalog.CatalogModelIDs(probe.Catalog, selection.Provider.Value), ", ")
			if available == "" {
				available = "none"
			}
			return []Finding{{
				Status:   StatusFail,
				Source:   string(field.value.Source),
				Evidence: fmt.Sprintf("%s=%s provider=%s available_models=%s", field.name, field.value.Value, selection.Provider.Value, available),
				Fix:      "select models from the Pi model catalog for the configured provider",
			}}
		}
	}
	return []Finding{{
		Status: StatusPass,
		Source: "pi",
		Evidence: fmt.Sprintf(
			"provider=%s planner=%s agent=%s small_tasks=%s",
			selection.Provider.Value,
			selection.PlannerModel.Value,
			selection.AgentModel.Value,
			selection.SmallTaskModel.Value,
		),
	}}
}

func MissingPiModelCatalogInputs(selection PiModelSelection) []string {
	var missing []string
	for field, value := range map[string]config.EffectiveString{
		"runtime_auth.provider": selection.Provider,
		"models.planner":        selection.PlannerModel,
		"models.agent":          selection.AgentModel,
		"models.small_tasks":    selection.SmallTaskModel,
	} {
		if !value.Set {
			missing = append(missing, field)
		}
	}
	slices.Sort(missing)
	return missing
}
