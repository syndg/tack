package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/recovery"
	"gopkg.in/yaml.v3"
)

// LoadFile loads a single blueprint from a YAML file.
func LoadFile(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint file %s: %w", path, err)
	}

	var bp Blueprint
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&bp); err != nil {
		return nil, fmt.Errorf("parsing blueprint YAML %s: %w", path, err)
	}

	if err := Validate(&bp); err != nil {
		return nil, fmt.Errorf("validating blueprint %s: %w", path, err)
	}

	return &bp, nil
}

// LoadDir loads all blueprints from a directory (non-recursive).
// Returns a map keyed by workflow ID.
func LoadDir(dir string) (map[string]*Blueprint, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint directory %s: %w", dir, err)
	}

	blueprints := make(map[string]*Blueprint)
	var errs []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		bp, err := LoadFile(path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}

		blueprints[bp.ID] = bp
	}

	if len(errs) > 0 {
		return blueprints, fmt.Errorf("errors loading blueprints: %s", strings.Join(errs, "; "))
	}

	return blueprints, nil
}

// Validate checks a blueprint for structural correctness:
//   - All steps have a unique ID
//   - All "next" references point to existing step IDs
//   - Agent steps have a role
//   - Deterministic steps have an action
//   - BlueprintRef steps have a ref
//   - No unreachable steps (except the first step, which is the entry point)
func Validate(bp *Blueprint) error {
	var errs []string

	if bp.ID == "" {
		errs = append(errs, "workflow id is required")
	}

	if len(bp.Steps) == 0 {
		errs = append(errs, "blueprint must have at least one step")
		return fmt.Errorf("blueprint validation failed: %s", strings.Join(errs, "; "))
	}

	if bp.Retry != nil {
		if !bp.Retry.Any() {
			errs = append(errs, "blueprint retry config cannot be empty")
		}
		if bp.Retry.Profile == "" {
			errs = append(errs, "blueprint retry profile is required when retry config is set")
		} else if !isValidRetryProfile(bp.Retry.Profile) {
			errs = append(errs, fmt.Sprintf("blueprint retry profile %q is invalid (must be strict, balanced, or self_healing)", bp.Retry.Profile))
		}
		if bp.Retry.DefaultMaxAttempts < 0 {
			errs = append(errs, "blueprint retry default_max_attempts must be greater than or equal to 0")
		}
		if bp.Retry.DefaultOnExhausted != "" && !isValidExhaustionMode(bp.Retry.DefaultOnExhausted) {
			errs = append(errs, fmt.Sprintf("blueprint retry default_on_exhausted %q is invalid (must be ask_human, escalate, or fail)", bp.Retry.DefaultOnExhausted))
		}
	}

	// Check unique step IDs and build ID set.
	stepIDs := make(map[string]bool, len(bp.Steps))
	for _, step := range bp.Steps {
		if step.ID == "" {
			errs = append(errs, "step has empty ID")
			continue
		}
		if stepIDs[step.ID] {
			errs = append(errs, fmt.Sprintf("duplicate step ID %q", step.ID))
		}
		stepIDs[step.ID] = true
	}

	// Build step-type lookup for cross-references.
	stepByID := make(map[string]Step, len(bp.Steps))
	for _, step := range bp.Steps {
		if step.ID != "" {
			stepByID[step.ID] = step
		}
	}

	// Check step-type-specific requirements and next references.
	for _, step := range bp.Steps {
		if step.Retry != nil {
			if !step.Retry.Any() {
				errs = append(errs, fmt.Sprintf("step %q retry config cannot be empty", step.ID))
			}
			if step.Retry.MaxAttempts < 0 {
				errs = append(errs, fmt.Sprintf("step %q retry max_attempts must be greater than or equal to 0", step.ID))
			}
			if step.Retry.OnExhausted != "" && !isValidExhaustionMode(step.Retry.OnExhausted) {
				errs = append(errs, fmt.Sprintf("step %q retry on_exhausted %q is invalid (must be ask_human, escalate, or fail)", step.ID, step.Retry.OnExhausted))
			}
			if step.Retry.HumanGuidanceMode != "" && !isValidHumanGuidanceMode(step.Retry.HumanGuidanceMode) {
				errs = append(errs, fmt.Sprintf("step %q retry human_guidance_mode %q is invalid (must be append or replace)", step.ID, step.Retry.HumanGuidanceMode))
			}
		}

		switch step.Type {
		case StepTypeAgent:
			if step.Role == "" {
				errs = append(errs, fmt.Sprintf("agent step %q must have a role", step.ID))
			}
			if step.Commit != "" && step.Commit != CommitModeAuto && step.Commit != CommitModeAgent && step.Commit != CommitModeNone {
				errs = append(errs, fmt.Sprintf("agent step %q has invalid commit mode %q (must be auto, agent, or none)", step.ID, step.Commit))
			}
			if step.Messages != nil && !step.Messages.Any() {
				errs = append(errs, fmt.Sprintf("agent step %q has empty messages config", step.ID))
			}
		case StepTypeDeterministic:
			if step.Action == "" {
				errs = append(errs, fmt.Sprintf("deterministic step %q must have an action", step.ID))
			}
			if step.Messages != nil {
				errs = append(errs, fmt.Sprintf("deterministic step %q cannot declare agent messages", step.ID))
			}
		case StepTypeBlueprintRef:
			if step.Ref == "" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q must have a ref", step.ID))
			}
			if step.Foreach != "" && step.Foreach != "work_item" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q has invalid foreach %q (must be work_item)", step.ID, step.Foreach))
			}
			if step.Messages != nil {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q cannot declare agent messages", step.ID))
			}
			if step.OnFail != "" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q cannot use on_fail (only deterministic steps can)", step.ID))
			}
			if step.OnWorkItemFailure != "" && step.OnWorkItemFailure != "escalate" && step.OnWorkItemFailure != "fail" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q has invalid on_work_item_failure %q (must be escalate or fail)", step.ID, step.OnWorkItemFailure))
			}
			if step.Escalation != nil {
				ctx := step.Escalation.Context
				if ctx != "" && ctx != "full" && ctx != "minimal" && ctx != "error_only" {
					errs = append(errs, fmt.Sprintf("blueprint_ref step %q has invalid escalation.context %q (must be full, minimal, or error_only)", step.ID, ctx))
				}
			}
		case StepTypeHuman:
			if step.Messages != nil {
				errs = append(errs, fmt.Sprintf("human step %q cannot declare agent messages", step.ID))
			}
			if step.OnFail != "" {
				errs = append(errs, fmt.Sprintf("human step %q cannot use on_fail (only deterministic steps can)", step.ID))
			}
		default:
			errs = append(errs, fmt.Sprintf("step %q has unknown type %q", step.ID, step.Type))
		}

		if step.Next != "" && !stepIDs[step.Next] {
			errs = append(errs, fmt.Sprintf("step %q references non-existent next step %q", step.ID, step.Next))
		}
		if step.MessageSource != "" && !stepIDs[step.MessageSource] {
			errs = append(errs, fmt.Sprintf("step %q references non-existent message source %q", step.ID, step.MessageSource))
		}
		if step.OnFail != "" {
			if !stepIDs[step.OnFail] {
				errs = append(errs, fmt.Sprintf("step %q references non-existent on_fail target %q", step.ID, step.OnFail))
			} else if target, ok := stepByID[step.OnFail]; ok && target.Type != StepTypeAgent {
				errs = append(errs, fmt.Sprintf("step %q on_fail target %q must be an agent step", step.ID, step.OnFail))
			}
		}
	}

	// Check for unreachable steps.
	// A step is reachable if it can be reached by following Next or OnFail
	// pointers starting from the first step (the entry point).
	reachable := make(map[string]bool, len(bp.Steps))
	var visit func(string)
	visit = func(stepID string) {
		if stepID == "" || reachable[stepID] {
			return
		}
		step, ok := stepByID[stepID]
		if !ok {
			return
		}
		reachable[stepID] = true
		visit(step.Next)
		visit(step.OnFail)
	}

	if len(bp.Steps) > 0 {
		visit(bp.Steps[0].ID)
	}

	for _, step := range bp.Steps {
		if step.ID != "" && !reachable[step.ID] {
			errs = append(errs, fmt.Sprintf("step %q is unreachable", step.ID))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("blueprint validation failed: %s", strings.Join(errs, "; "))
	}

	return nil
}

func isValidRetryProfile(profile recovery.Profile) bool {
	switch profile {
	case recovery.ProfileStrict, recovery.ProfileBalanced, recovery.ProfileSelfHealing:
		return true
	default:
		return false
	}
}

func isValidExhaustionMode(mode domain.ExhaustionMode) bool {
	switch mode {
	case domain.ExhaustionAskHuman, domain.ExhaustionEscalate, domain.ExhaustionFail:
		return true
	default:
		return false
	}
}

func isValidHumanGuidanceMode(mode string) bool {
	switch mode {
	case "append", "replace":
		return true
	default:
		return false
	}
}
