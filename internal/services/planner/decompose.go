package planner

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/syndg/deck/internal/domain"
	"gopkg.in/yaml.v3"
)

// RawPlan is the YAML structure a planner agent outputs.
// Parsed from the agent's output, validated, and converted to domain types.
type RawPlan struct {
	Streams      []RawStream `yaml:"streams"`
	QualityGates []string    `yaml:"quality_gates"`
}

type RawStream struct {
	Title        string   `yaml:"title"`
	Description  string   `yaml:"description"`
	FileScope    []string `yaml:"file_scope"`
	Dependencies []string `yaml:"dependencies"` // stream titles or indices
}

// ParsePlan extracts a RawPlan from agent output text.
// Tries multiple strategies in order:
// 1. Fenced ```yaml ... ``` code blocks (tries all blocks found)
// 2. Inline YAML starting from "streams:" marker
// 3. Entire output as raw YAML
func ParsePlan(agentOutput string) (*RawPlan, error) {
	// Strategy 1: Try all fenced YAML code blocks
	for _, block := range extractAllYAMLBlocks(agentOutput) {
		var plan RawPlan
		if err := yaml.Unmarshal([]byte(block), &plan); err == nil && len(plan.Streams) > 0 {
			return &plan, nil
		}
	}

	// Strategy 2: Find "streams:" marker and try from there
	if idx := strings.Index(agentOutput, "\nstreams:"); idx >= 0 {
		candidate := agentOutput[idx+1:]
		var plan RawPlan
		if err := yaml.Unmarshal([]byte(candidate), &plan); err == nil && len(plan.Streams) > 0 {
			return &plan, nil
		}
	}
	// Also try if the output starts with "streams:"
	if strings.HasPrefix(strings.TrimSpace(agentOutput), "streams:") {
		trimmed := strings.TrimSpace(agentOutput)
		var plan RawPlan
		if err := yaml.Unmarshal([]byte(trimmed), &plan); err == nil && len(plan.Streams) > 0 {
			return &plan, nil
		}
	}

	// Strategy 3: Try entire output as YAML
	var plan RawPlan
	if err := yaml.Unmarshal([]byte(agentOutput), &plan); err != nil {
		return nil, fmt.Errorf("parsing plan: %w", err)
	}
	if len(plan.Streams) == 0 {
		return nil, fmt.Errorf("parsing plan: no streams found in output")
	}
	return &plan, nil
}

// extractAllYAMLBlocks finds all ```yaml ... ``` fenced code blocks in the text.
// Returns the content inside each block (without the fences).
func extractAllYAMLBlocks(s string) []string {
	const openFence = "```yaml"
	const closeFence = "```"

	var blocks []string
	remaining := s

	for {
		start := strings.Index(remaining, openFence)
		if start == -1 {
			break
		}

		contentStart := start + len(openFence)
		if contentStart < len(remaining) && remaining[contentStart] == '\n' {
			contentStart++
		}

		end := strings.Index(remaining[contentStart:], closeFence)
		if end == -1 {
			break
		}

		blocks = append(blocks, remaining[contentStart:contentStart+end])
		remaining = remaining[contentStart+end+len(closeFence):]
	}

	return blocks
}

// ValidatePlan checks a raw plan for structural correctness:
// - At least one stream
// - All streams have a title
// - All streams have a non-empty file_scope
// - Dependency references point to existing stream titles or 1-based indices
// - No circular dependencies
// Returns all validation errors joined.
func ValidatePlan(plan *RawPlan) error {
	var errs []string

	if len(plan.Streams) == 0 {
		return errors.New("plan must have at least one stream")
	}

	for i, s := range plan.Streams {
		if s.Title == "" {
			errs = append(errs, fmt.Sprintf("stream %d missing title", i))
		}
		if len(s.FileScope) == 0 {
			name := s.Title
			if name == "" {
				name = fmt.Sprintf("%d", i)
			}
			errs = append(errs, fmt.Sprintf("stream %q missing file_scope", name))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	for _, s := range plan.Streams {
		for _, dep := range s.Dependencies {
			if _, ok := resolveDependencyTitle(dep, plan.Streams); !ok {
				errs = append(errs, fmt.Sprintf("stream %q dependency %q not found", s.Title, dep))
			}
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	if err := DetectCycles(plan.Streams); err != nil {
		return err
	}

	return nil
}

// ToDomain converts a RawPlan to domain Plan + Streams.
// Generates IDs, resolves dependency titles or indices to stream IDs,
// sets initial statuses.
func ToDomain(raw *RawPlan, objectiveID string) (*domain.Plan, []domain.Stream) {
	now := time.Now()

	plan := &domain.Plan{
		ID:           uuid.New().String(),
		ObjectiveID:  objectiveID,
		Status:       domain.PlanStatusDraft,
		QualityGates: raw.QualityGates,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if plan.QualityGates == nil {
		plan.QualityGates = []string{}
	}

	// First pass: assign IDs to each stream, build title→ID map.
	titleToID := make(map[string]string, len(raw.Streams))
	streamIDs := make([]string, len(raw.Streams))
	for i, rs := range raw.Streams {
		id := uuid.New().String()
		streamIDs[i] = id
		titleToID[rs.Title] = id
	}

	// Second pass: build domain.Stream values with resolved dependency IDs.
	streams := make([]domain.Stream, len(raw.Streams))
	for i, rs := range raw.Streams {
		deps := make([]string, 0, len(rs.Dependencies))
		for _, depRef := range rs.Dependencies {
			if depTitle, ok := resolveDependencyTitle(depRef, raw.Streams); ok {
				if depID, ok := titleToID[depTitle]; ok {
					deps = append(deps, depID)
				}
			}
		}

		scope := rs.FileScope
		if scope == nil {
			scope = []string{}
		}

		streams[i] = domain.Stream{
			ID:           streamIDs[i],
			PlanID:       plan.ID,
			Title:        rs.Title,
			Description:  rs.Description,
			FileScope:    scope,
			Dependencies: deps,
			Status:       domain.StreamStatusPending,
			CreatedAt:    now,
		}
	}

	return plan, streams
}

// DetectCycles checks the dependency graph for cycles using DFS.
func DetectCycles(streams []RawStream) error {
	adj := make(map[string][]string, len(streams))
	for _, s := range streams {
		deps := make([]string, 0, len(s.Dependencies))
		for _, depRef := range s.Dependencies {
			depTitle, ok := resolveDependencyTitle(depRef, streams)
			if !ok {
				return fmt.Errorf("stream %q dependency %q not found", s.Title, depRef)
			}
			deps = append(deps, depTitle)
		}
		adj[s.Title] = deps
	}

	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(streams))

	var dfs func(node string) error
	dfs = func(node string) error {
		if state[node] == visiting {
			return fmt.Errorf("circular dependency detected at stream %q", node)
		}
		if state[node] == visited {
			return nil
		}
		state[node] = visiting
		for _, dep := range adj[node] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		state[node] = visited
		return nil
	}

	for _, s := range streams {
		if err := dfs(s.Title); err != nil {
			return err
		}
	}
	return nil
}

func resolveDependencyTitle(ref string, streams []RawStream) (string, bool) {
	ref = strings.TrimSpace(ref)
	for _, stream := range streams {
		if stream.Title == ref {
			return stream.Title, true
		}
	}

	idx, err := strconv.Atoi(ref)
	if err != nil || idx < 1 || idx > len(streams) {
		return "", false
	}
	return streams[idx-1].Title, true
}
