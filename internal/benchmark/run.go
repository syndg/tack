package benchmark

import (
	"time"

	"github.com/google/uuid"
	"github.com/syndg/tack/internal/version"
)

type Run struct {
	ID                string
	BenchmarkID       string
	RunID             string
	Status            string
	DisplayName       string
	Repo              string
	Family            string
	ReadinessSnapshot string
	Baseline          string
	ContextPolicy     string
	PromptSnapshot    string
	RequestedMode     string
	EffectiveMode     string
	Blueprint         string
	Workspace         string
	ProjectID         string
	ObjectiveID       string
	StatusReason      string
	ScoreStatus       string
	Version           string
	CreatedAt         string
}

func PrepareRun(id, requestedMode, blueprint string) (Run, bool) {
	spec, ok := FindSpec(id)
	if !ok {
		return Run{}, false
	}
	effectiveMode := requestedMode
	if effectiveMode == "" {
		effectiveMode = "auto"
	}
	if blueprint == "" {
		blueprint = spec.Blueprint
	}
	if blueprint == "" {
		blueprint = "standard"
	}
	return Run{
		ID:                uuid.NewString(),
		BenchmarkID:       spec.ID,
		Status:            "planned",
		DisplayName:       spec.DisplayName,
		Repo:              spec.Repo,
		Family:            spec.Family,
		ReadinessSnapshot: spec.Readiness(),
		Baseline:          spec.Baseline,
		ContextPolicy:     spec.ContextPolicy,
		PromptSnapshot:    spec.Prompt,
		RequestedMode:     effectiveMode,
		EffectiveMode:     effectiveMode,
		Blueprint:         blueprint,
		ScoreStatus:       "pending",
		Version:           version.Version,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
	}, true
}
