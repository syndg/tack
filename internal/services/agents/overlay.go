package agents

import (
	"fmt"
	"strings"

	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/blueprint"
	"github.com/syndg/deck/internal/harness/rules"
	"github.com/syndg/deck/internal/harness/tools"
)

// OverlayInput holds all inputs for constructing an agent's system overlay.
type OverlayInput struct {
	AgentName    string                     // e.g., "builder-auth-1"
	Role         *RoleDefinition            // role definition for this agent
	Objective    *domain.Objective          // the objective being executed
	Stream       *domain.Stream             // nil for planner
	TaskSpec     string                     // from lead or plan description
	FileScope    []string                   // files this agent may modify
	MatchedRules []rules.MatchedRule        // rules matched against file scope
	CuratedTools tools.CurationResult       // resolved tool set
	QualityGates []string                   // gate commands to run before completion
	LeadAgent    string                     // name of this agent's lead (empty for planners)
	Guidance     string                     // project-level guidance from .deck/config.yaml
	CommitMode   string                     // "auto", "agent", "none" — controls commit behavior
	Messages     *blueprint.MessageRequests // optional delivery messages to generate
	FixContext   string                     // quality gate errors from a previous fix-loop iteration
}

// BuildOverlay generates the markdown system prompt overlay for an agent.
// Sections:
//  1. Agent identity and role
//  2. Task description
//  3. File scope (if any)
//  4. Matched rules (high-priority prefixed with "IMPORTANT CONSTRAINT:")
//  5. Quality gates
//  6. Communication config
//  7. Delivery metadata (optional)
//  8. Commit policy/instructions
//  9. Constraints
func BuildOverlay(input OverlayInput) string {
	var b strings.Builder

	// 1. Agent identity and role
	fmt.Fprintf(&b, "# Deck Agent: %s\n\n", input.AgentName)
	b.WriteString("## Role\n")
	fmt.Fprintf(&b, "You are a %s agent. %s\n\n", input.Role.Name, input.Role.Description)

	// 2. Task description
	b.WriteString("## Task\n")
	fmt.Fprintf(&b, "Objective: %s\n", input.Objective.Description)
	if input.Stream != nil {
		fmt.Fprintf(&b, "Stream: %s\n", input.Stream.Title)
	}
	if input.TaskSpec != "" {
		fmt.Fprintf(&b, "%s\n", input.TaskSpec)
	}
	b.WriteString("\n")

	// 2b. Fix context (when agent is re-running after gate failure)
	if input.FixContext != "" {
		b.WriteString("## Fix Context\n")
		b.WriteString("Your previous changes failed quality gates. Fix the errors below:\n\n")
		b.WriteString("```\n")
		b.WriteString(input.FixContext)
		b.WriteString("\n```\n\n")
		b.WriteString("Your previous code is still in the worktree. Fix the failing issues and ensure quality gates pass.\n\n")
	}

	// 3. File scope (conditional)
	if len(input.FileScope) > 0 {
		b.WriteString("## File Scope\n")
		b.WriteString("You may ONLY modify these files:\n")
		for _, f := range input.FileScope {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	// 4. Matched rules
	if len(input.MatchedRules) > 0 {
		b.WriteString("## Rules\n")
		for _, mr := range input.MatchedRules {
			if mr.Rule.Priority == "high" {
				fmt.Fprintf(&b, "IMPORTANT CONSTRAINT: %s\n\n", strings.TrimSpace(mr.Rule.Body))
			} else {
				fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(mr.Rule.Body))
			}
		}
	}

	// 5. Quality gates
	if len(input.QualityGates) > 0 {
		b.WriteString("## Quality Gates\n")
		b.WriteString("Before signaling completion, you MUST pass:\n")
		for _, gate := range input.QualityGates {
			fmt.Fprintf(&b, "- %s\n", gate)
		}
		b.WriteString("\n")
	}

	// 6. Communication config
	b.WriteString("## Communication\n")
	if input.LeadAgent != "" {
		fmt.Fprintf(&b, "- Your lead is: %s\n", input.LeadAgent)
	}
	b.WriteString("- Use deck.status() to report progress\n")
	b.WriteString("- Use deck.escalate() if you're blocked\n")
	b.WriteString("- Use deck.done() when finished\n\n")

	// 7. Delivery metadata (optional, based on requested messages)
	if input.Messages != nil && input.Messages.Any() {
		b.WriteString("## Delivery Metadata\n")
		b.WriteString("At the very end of your final response, emit exactly one line starting with `DECK_MESSAGES:` followed by compact JSON containing the requested fields below.\n")
		b.WriteString("Do not wrap it in a code fence. Keep it on a single line so Deck can parse it reliably.\n")
		b.WriteString("If your runtime supports deck.done(), include that same final `DECK_MESSAGES:` line in the summary you pass to deck.done().\n")
		if input.CommitMode == "agent" {
			b.WriteString("If you are committing manually, you may write the commit first, then emit the final DECK_MESSAGES line in your response.\n")
		}
		b.WriteString("\nRequested fields:\n")
		if input.Messages.Commit {
			b.WriteString("- `commit_message`: the exact git commit message Deck should use (subject line with optional body)\n")
		}
		if input.Messages.PR {
			b.WriteString("- `pr_title`: concise pull request title\n")
			b.WriteString("- `pr_body`: markdown pull request body summarizing the change, testing, and context\n")
		}
		b.WriteString("\nExample final line:\n")
		fields := RequestedMessageFieldNames(input.Messages)
		b.WriteString("`DECK_MESSAGES:{")
		for i, field := range fields {
			if i > 0 {
				b.WriteString(",")
			}
			sample := "Describe this field"
			switch field {
			case MetadataKeyCommitMessage:
				sample = "feat: implement the requested change"
			case MetadataKeyPRTitle:
				sample = "Implement the requested change"
			case MetadataKeyPRBody:
				sample = "## Summary\\n- What changed\\n\\n## Testing\\n- go test ./..."
			}
			fmt.Fprintf(&b, "\"%s\":\"%s\"", field, sample)
		}
		b.WriteString("}`\n\n")
	}

	// 8. Commit instructions (based on commit mode)
	switch input.CommitMode {
	case "agent":
		b.WriteString("## Commit Instructions\n")
		b.WriteString("When you are done with your changes, you MUST commit them:\n")
		b.WriteString("1. Stage all code changes\n")
		b.WriteString("2. Write a clear, descriptive commit message summarizing what you changed and why\n")
		b.WriteString("3. Run: git commit -m \"<your message>\"\n")
		b.WriteString("4. Do NOT push — Deck handles merging\n\n")
	case "none":
		// No commit instructions for analysis/scout agents.
	default: // "auto" or empty
		b.WriteString("## Commit Policy\n")
		b.WriteString("- Do NOT run git add or git commit — Deck commits your changes automatically\n\n")
	}

	// 9. Constraints
	b.WriteString("## Constraints\n")
	b.WriteString("- Do NOT modify files outside your scope\n")
	b.WriteString("- Do NOT push to git (Deck handles merging)\n")
	b.WriteString("- Do NOT install new dependencies without escalating\n")

	return b.String()
}

// planYAMLSchema is the YAML format a planner agent must output.
const planYAMLSchema = `streams:
  - title: "stream title"
    description: "what this stream does"
    file_scope:
      - "src/auth/**"
    dependencies: []  # titles of streams that must complete first
quality_gates:
  - "go test ./..."
  - "go vet ./..."`

// BuildPlannerOverlay generates a simplified overlay for planner agents.
// Planners get: role, objective description, guidance, and instructions
// for producing a structured plan with streams, scopes, and dependencies.
func BuildPlannerOverlay(objective *domain.Objective, guidance string) string {
	var b strings.Builder

	b.WriteString("# Deck Agent: planner\n\n")

	b.WriteString("## Role\n")
	b.WriteString("You are a Planner agent. Explore the codebase and decompose the objective into parallel work streams.\n\n")

	b.WriteString("## Objective\n")
	fmt.Fprintf(&b, "%s\n\n", objective.Description)

	if guidance != "" {
		b.WriteString("## Project Guidance\n")
		fmt.Fprintf(&b, "%s\n\n", guidance)
	}

	b.WriteString("## Instructions\n")
	b.WriteString("Produce a structured plan with:\n")
	b.WriteString("1. Streams — parallel units of work\n")
	b.WriteString("2. File scopes — which files each stream owns (use globs)\n")
	b.WriteString("3. Dependencies — which streams must complete before others start\n")
	b.WriteString("4. Quality gates — commands to validate each stream\n\n")
	b.WriteString("Output your plan as YAML in the following format:\n")
	fmt.Fprintf(&b, "```yaml\n%s\n```\n", planYAMLSchema)

	return b.String()
}
