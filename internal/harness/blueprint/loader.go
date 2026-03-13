package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile loads a single blueprint from a YAML file.
func LoadFile(path string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint file %s: %w", path, err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("parsing blueprint YAML %s: %w", path, err)
	}

	if err := Validate(&bp); err != nil {
		return nil, fmt.Errorf("validating blueprint %s: %w", path, err)
	}

	return &bp, nil
}

// LoadDir loads all blueprints from a directory (non-recursive).
// Returns a map keyed by blueprint name.
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

		blueprints[bp.Name] = bp
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

	if bp.Name == "" {
		errs = append(errs, "blueprint name is required")
	}

	if len(bp.Steps) == 0 {
		errs = append(errs, "blueprint must have at least one step")
		return fmt.Errorf("blueprint validation failed: %s", strings.Join(errs, "; "))
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
			if step.OnFail != "" {
				errs = append(errs, fmt.Sprintf("agent step %q cannot use on_fail (only deterministic steps can)", step.ID))
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
			if step.Messages != nil {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q cannot declare agent messages", step.ID))
			}
			if step.OnFail != "" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q cannot use on_fail (only deterministic steps can)", step.ID))
			}
			if step.OnStreamFailure != "" && step.OnStreamFailure != "escalate" && step.OnStreamFailure != "fail" {
				errs = append(errs, fmt.Sprintf("blueprint_ref step %q has invalid on_stream_failure %q (must be escalate or fail)", step.ID, step.OnStreamFailure))
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
