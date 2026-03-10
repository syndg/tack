package planner

import (
	"errors"
	"fmt"
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
// Looks for a YAML block delimited by ```yaml ... ``` markers.
// Falls back to trying the entire output as YAML.
func ParsePlan(agentOutput string) (*RawPlan, error) {
	if block, ok := extractYAMLBlock(agentOutput); ok {
		var plan RawPlan
		if err := yaml.Unmarshal([]byte(block), &plan); err == nil {
			return &plan, nil
		}
	}

	// Fallback: try entire output as YAML.
	var plan RawPlan
	if err := yaml.Unmarshal([]byte(agentOutput), &plan); err != nil {
		return nil, fmt.Errorf("parsing plan: %w", err)
	}
	return &plan, nil
}

// extractYAMLBlock scans for a ```yaml ... ``` fenced code block.
// Returns the content inside (without the fences) and true if found.
func extractYAMLBlock(s string) (string, bool) {
	const openFence = "```yaml"
	const closeFence = "```"

	start := strings.Index(s, openFence)
	if start == -1 {
		return "", false
	}
	// Skip past the opening fence and optional newline.
	contentStart := start + len(openFence)
	if contentStart < len(s) && s[contentStart] == '\n' {
		contentStart++
	}

	end := strings.Index(s[contentStart:], closeFence)
	if end == -1 {
		return "", false
	}

	return s[contentStart : contentStart+end], true
}

// ValidatePlan checks a raw plan for structural correctness:
// - At least one stream
// - All streams have a title
// - All streams have a non-empty file_scope
// - Dependency references point to existing stream titles
// - No circular dependencies
// Returns all validation errors joined.
func ValidatePlan(plan *RawPlan) error {
	var errs []string

	if len(plan.Streams) == 0 {
		return errors.New("plan must have at least one stream")
	}

	titles := make(map[string]bool, len(plan.Streams))
	for i, s := range plan.Streams {
		if s.Title == "" {
			errs = append(errs, fmt.Sprintf("stream %d missing title", i))
		} else {
			titles[s.Title] = true
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

	// Validate dependency references.
	for _, s := range plan.Streams {
		for _, dep := range s.Dependencies {
			if !titles[dep] {
				errs = append(errs, fmt.Sprintf("stream %q dependency %q not found", s.Title, dep))
			}
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	// Check for cycles.
	if err := DetectCycles(plan.Streams); err != nil {
		return err
	}

	return nil
}

// ToDomain converts a RawPlan to domain Plan + Streams.
// Generates IDs, resolves dependency titles to stream IDs,
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
		for _, depTitle := range rs.Dependencies {
			if depID, ok := titleToID[depTitle]; ok {
				deps = append(deps, depID)
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
			Status:       "pending",
			CreatedAt:    now,
		}
	}

	return plan, streams
}

// DetectCycles checks the dependency graph for cycles using DFS.
func DetectCycles(streams []RawStream) error {
	// Build adjacency list: title → dependencies.
	adj := make(map[string][]string, len(streams))
	for _, s := range streams {
		adj[s.Title] = s.Dependencies
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
