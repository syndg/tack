package blueprintconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/harness/blueprint"
	"gopkg.in/yaml.v3"
)

const StandardBlueprintID = "standard"

type StepRow struct {
	ID           string
	Type         blueprint.StepType
	Role         string
	Action       string
	Ref          string
	Dependencies []string
	Provides     []string
	Requirements []string
}

type Requirements struct {
	Runtime      bool `json:"runtime" yaml:"runtime"`
	RuntimeAuth  bool `json:"runtime_auth" yaml:"runtime_auth"`
	Sandbox      bool `json:"sandbox" yaml:"sandbox"`
	Git          bool `json:"git" yaml:"git"`
	QualityGates bool `json:"quality_gates" yaml:"quality_gates"`
	CreatePR     bool `json:"create_pr" yaml:"create_pr"`
}

type BlueprintLookup interface {
	GetBlueprint(id string) (*blueprint.Blueprint, bool)
}

func (r Requirements) Names() []string {
	var names []string
	if r.Runtime {
		names = append(names, "runtime")
	}
	if r.RuntimeAuth {
		names = append(names, "runtime_auth")
	}
	if r.Sandbox {
		names = append(names, "sandbox")
	}
	if r.Git {
		names = append(names, "git")
	}
	if r.QualityGates {
		names = append(names, "quality_gates")
	}
	if r.CreatePR {
		names = append(names, "create_pr")
	}
	return names
}

func LoadShippedStandard() (*blueprint.Blueprint, error) {
	reg := blueprint.NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		return nil, err
	}
	bp, ok := reg.Get(StandardBlueprintID)
	if !ok {
		return nil, fmt.Errorf("shipped %q blueprint not found", StandardBlueprintID)
	}
	return Clone(bp), nil
}

func LoadActiveRegistry(userConfigPath, projectRoot string) (*blueprint.Registry, error) {
	reg := blueprint.NewRegistry()
	if err := reg.LoadDefaults(); err != nil {
		return nil, fmt.Errorf("loading default blueprints: %w", err)
	}
	for _, dir := range []string{UserBlueprintsDir(userConfigPath), ProjectBlueprintsDir(projectRoot)} {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("checking blueprint directory %s: %w", dir, err)
		}
		if !info.IsDir() {
			continue
		}
		if err := reg.LoadFromDir(dir); err != nil {
			return nil, fmt.Errorf("loading blueprint directory %s: %w", dir, err)
		}
	}
	return reg, nil
}

func UserBlueprintsDir(userConfigPath string) string {
	if strings.TrimSpace(userConfigPath) == "" {
		userConfigPath = config.UserConfigPath
	}
	return filepath.Join(filepath.Dir(expandPath(userConfigPath)), "blueprints")
}

func ProjectBlueprintsDir(projectRoot string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, config.ProjectConfigDir, "blueprints")
}

func Clone(bp *blueprint.Blueprint) *blueprint.Blueprint {
	if bp == nil {
		return nil
	}
	clone := *bp
	clone.Steps = append([]blueprint.Step(nil), bp.Steps...)
	return &clone
}

func Rows(bp *blueprint.Blueprint) []StepRow {
	if bp == nil {
		return nil
	}
	deps := dependencies(bp)
	rows := make([]StepRow, 0, len(bp.Steps))
	for _, step := range bp.Steps {
		rows = append(rows, StepRow{
			ID:           step.ID,
			Type:         step.Type,
			Role:         step.Role,
			Action:       step.Action,
			Ref:          step.Ref,
			Dependencies: deps[step.ID],
			Provides:     provides(step),
			Requirements: requirementsForStep(step).Names(),
		})
	}
	return rows
}

func CandidateFromRemoved(bp *blueprint.Blueprint, removed []string) (*blueprint.Blueprint, error) {
	remove := map[string]bool{}
	for _, id := range removed {
		id = strings.TrimSpace(id)
		if id != "" {
			remove[id] = true
		}
	}
	return Candidate(bp, remove)
}

func Candidate(bp *blueprint.Blueprint, remove map[string]bool) (*blueprint.Blueprint, error) {
	if bp == nil {
		return nil, fmt.Errorf("blueprint is required")
	}
	if err := explainUnsupportedRemoval(bp, remove); err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(bp.Steps))
	known := make(map[string]bool, len(bp.Steps))
	for _, step := range bp.Steps {
		known[step.ID] = true
		if !remove[step.ID] {
			keep[step.ID] = true
		}
	}
	for id := range remove {
		if !known[id] {
			return nil, fmt.Errorf("step %q does not exist in blueprint %q", id, bp.ID)
		}
	}
	candidate := Clone(bp)
	candidate.Steps = candidate.Steps[:0]
	for _, step := range bp.Steps {
		if remove[step.ID] {
			continue
		}
		step.Next = nextKept(bp, step.Next, keep, remove)
		if step.OnFail != "" && !keep[step.OnFail] {
			return nil, fmt.Errorf("step %q requires removed on_fail step %q", step.ID, step.OnFail)
		}
		if step.MessageSource != "" && !keep[step.MessageSource] {
			return nil, fmt.Errorf("step %q requires removed message_source step %q", step.ID, step.MessageSource)
		}
		candidate.Steps = append(candidate.Steps, step)
	}
	if err := blueprint.Validate(candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func ExtractRequirements(bp *blueprint.Blueprint) Requirements {
	var req Requirements
	if bp == nil {
		return req
	}
	for _, step := range bp.Steps {
		req = mergeRequirements(req, requirementsForStep(step))
	}
	return req
}

func ExtractRequirementsFromLookup(lookup BlueprintLookup, blueprintID string) (Requirements, error) {
	var req Requirements
	steps, err := StepsForBlueprint(lookup, blueprintID)
	if err != nil {
		return req, err
	}
	for _, step := range steps {
		req = mergeRequirements(req, requirementsForStep(step))
	}
	return req, nil
}

func StepsForBlueprint(lookup BlueprintLookup, blueprintID string) ([]blueprint.Step, error) {
	if lookup == nil {
		return nil, fmt.Errorf("blueprint lookup is unavailable")
	}
	seen := map[string]bool{}
	var steps []blueprint.Step
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		bp, ok := lookup.GetBlueprint(id)
		if !ok {
			return fmt.Errorf("blueprint %q not found", id)
		}
		for _, step := range bp.Steps {
			steps = append(steps, step)
			if step.Type == blueprint.StepTypeBlueprintRef && step.Ref != "" {
				if err := visit(step.Ref); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(blueprintID); err != nil {
		return nil, err
	}
	return steps, nil
}

func SaveGlobalOverride(path string, bp *blueprint.Blueprint) error {
	if bp == nil {
		return fmt.Errorf("blueprint is required")
	}
	if bp.ID != StandardBlueprintID {
		return fmt.Errorf("global override must keep blueprint id %q", StandardBlueprintID)
	}
	if err := blueprint.Validate(bp); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating blueprint directory: %w", err)
	}
	data, err := yaml.Marshal(bp)
	if err != nil {
		return fmt.Errorf("marshaling blueprint: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing blueprint override: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("installing blueprint override: %w", err)
	}
	return nil
}

func dependencies(bp *blueprint.Blueprint) map[string][]string {
	deps := map[string][]string{}
	for _, step := range bp.Steps {
		if step.Next != "" {
			deps[step.Next] = append(deps[step.Next], step.ID+".next")
		}
		if step.OnFail != "" {
			deps[step.OnFail] = append(deps[step.OnFail], step.ID+".on_fail")
		}
		if step.MessageSource != "" {
			deps[step.MessageSource] = append(deps[step.MessageSource], step.ID+".message_source")
		}
	}
	for id := range deps {
		sort.Strings(deps[id])
	}
	return deps
}

func provides(step blueprint.Step) []string {
	switch step.Action {
	case "dispatch_streams":
		return []string{"work_items"}
	case "merge_queue":
		return []string{"merged_branch"}
	case "create_pr":
		return []string{"pull_request"}
	case "mark_complete":
		return []string{"completion"}
	}
	if step.Type == blueprint.StepTypeAgent {
		return []string{"agent_output"}
	}
	return nil
}

func requirementsForStep(step blueprint.Step) Requirements {
	var req Requirements
	switch step.Type {
	case blueprint.StepTypeAgent:
		req.Runtime = true
		req.RuntimeAuth = true
		req.Sandbox = true
	case blueprint.StepTypeBlueprintRef:
		req.Runtime = true
		req.RuntimeAuth = true
		req.Sandbox = true
	}
	switch step.Action {
	case "dispatch_streams":
		req.Sandbox = true
	case "merge_queue":
		req.Git = true
		req.QualityGates = true
	case "create_pr":
		req.Git = true
		req.CreatePR = true
	}
	return req
}

func mergeRequirements(a, b Requirements) Requirements {
	a.Runtime = a.Runtime || b.Runtime
	a.RuntimeAuth = a.RuntimeAuth || b.RuntimeAuth
	a.Sandbox = a.Sandbox || b.Sandbox
	a.Git = a.Git || b.Git
	a.QualityGates = a.QualityGates || b.QualityGates
	a.CreatePR = a.CreatePR || b.CreatePR
	return a
}

func explainUnsupportedRemoval(bp *blueprint.Blueprint, remove map[string]bool) error {
	if !remove["plan"] {
		return nil
	}
	var remaining []string
	for _, step := range bp.Steps {
		if step.ID != "plan" && !remove[step.ID] {
			remaining = append(remaining, step.ID)
		}
	}
	if len(remaining) == 0 {
		return nil
	}
	return fmt.Errorf("step %q cannot be removed while dependent steps remain: %s", "plan", strings.Join(remaining, ", "))
}

func nextKept(bp *blueprint.Blueprint, id string, keep, remove map[string]bool) string {
	seen := map[string]bool{}
	for id != "" && remove[id] && !seen[id] {
		seen[id] = true
		id = stepNext(bp, id)
	}
	if keep[id] {
		return id
	}
	return ""
}

func stepNext(bp *blueprint.Blueprint, id string) string {
	for _, step := range bp.Steps {
		if step.ID == id {
			return step.Next
		}
	}
	return ""
}

func expandPath(path string) string {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
