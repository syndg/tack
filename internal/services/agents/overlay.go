package agents

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/codification"
	"github.com/syndg/tack/internal/contractpatch"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
	"github.com/syndg/tack/internal/harness/tools"
)

// OverlayInput holds all inputs for constructing an agent's system overlay.
type OverlayInput struct {
	AgentName         string                     // e.g., "builder-auth-1"
	Role              *RoleDefinition            // role definition for this agent
	Objective         *domain.Objective          // the objective being executed
	Stream            *domain.Stream             // nil for planner
	TaskSpec          string                     // from lead or plan description
	FileScope         []string                   // files this agent may modify
	MatchedRules      []rules.MatchedRule        // rules matched against file scope
	CuratedTools      tools.CurationResult       // resolved tool set
	QualityGates      []string                   // gate commands to run before completion
	LeadAgent         string                     // name of this agent's lead (empty for planners)
	Guidance          string                     // project-level guidance from .tack/config.yaml
	CommitMode        string                     // "auto", "agent", "none" — controls commit behavior
	Messages          *blueprint.MessageRequests // optional delivery messages to generate
	FixContext        string                     // quality gate errors from a previous fix-loop iteration
	RetryContext      *RetryContext              // normalized recovery context for reruns
	ObjectiveInsights []domain.ObjectiveInsight  // durable objective-local insights
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
	fmt.Fprintf(&b, "# Tack Agent: %s\n\n", input.AgentName)
	b.WriteString("## Role\n")
	fmt.Fprintf(&b, "You are a %s agent. %s\n\n", input.Role.Name, input.Role.Description)

	// 2. Task description
	b.WriteString("## Task\n")
	fmt.Fprintf(&b, "Objective: %s\n", input.Objective.Description)
	var card domain.StreamCard
	hasCard := false
	if input.Stream != nil {
		fmt.Fprintf(&b, "Stream: %s\n", input.Stream.Title)
		card = input.Stream.EffectiveCard()
		hasCard = true
		if strings.TrimSpace(card.Goal) != "" {
			fmt.Fprintf(&b, "Goal: %s\n", card.Goal)
		}
	}
	if input.TaskSpec != "" {
		fmt.Fprintf(&b, "%s\n", input.TaskSpec)
	}
	b.WriteString("\n")
	if hasCard && len(card.BlockedBy) > 0 {
		b.WriteString("## Blocked By\n")
		for _, dep := range card.BlockedBy {
			fmt.Fprintf(&b, "- %s\n", dep)
		}
		b.WriteString("\n")
	}
	if hasCard && len(card.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n")
		b.WriteString("The work is only complete when all of the following are true:\n")
		for _, item := range card.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if hasCard && len(card.ImplementationScope) > 0 {
		b.WriteString("## Implementation Scope\n")
		b.WriteString("Implement only within these bounded areas:\n")
		for _, item := range card.ImplementationScope {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if hasCard && len(card.ProofScope) > 0 {
		b.WriteString("## Proof Scope\n")
		b.WriteString("Your completion notes and validation should cover:\n")
		for _, item := range card.ProofScope {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if hasCard && len(card.HardAnchors) > 0 {
		b.WriteString("## Hard Anchors\n")
		b.WriteString("These are hard implementation constraints from planning. Do not reinterpret them away.\n")
		for _, anchor := range card.HardAnchors {
			fmt.Fprintf(&b, "- %s\n", anchor.Instruction)
			for _, citation := range anchor.Citations {
				fmt.Fprintf(&b, "  Evidence: [%s] %s %s - %s\n", citation.ID, citation.Kind, citation.Target, citation.Detail)
			}
		}
		b.WriteString("\n")
	}
	if hasCard && strings.TrimSpace(card.SeamOverrideRationale) != "" {
		b.WriteString("## Seam Override Rationale\n")
		b.WriteString(card.SeamOverrideRationale)
		b.WriteString("\n\n")
	}
	patches := card.ContractPatches
	if len(patches) == 0 {
		streamID := ""
		projectID := ""
		objectiveID := ""
		if input.Objective != nil {
			projectID = input.Objective.ProjectID
			objectiveID = input.Objective.ID
		}
		if input.Stream != nil {
			streamID = input.Stream.ID
		}
		patches = contractpatch.Merge(
			contractpatch.Compile(input.ObjectiveInsights, streamID),
			codification.AutoAppliedPatches(projectID, objectiveID, input.ObjectiveInsights),
		)
	}
	if len(patches) > 0 {
		writeContractPatches(&b, patches, "Treat these as additive contract clarifications already learned during this objective.")
	}
	if len(input.ObjectiveInsights) > 0 {
		b.WriteString("## Objective Insights\n")
		b.WriteString("Recent objective-local signals already learned during this run:\n")
		for _, insight := range input.ObjectiveInsights {
			line := fmt.Sprintf("- [%s/%s] %s", insight.Source, insight.Kind, insight.Summary)
			if insight.StreamID != "" && (input.Stream == nil || insight.StreamID != input.Stream.ID) {
				line += fmt.Sprintf(" (stream %s)", insight.StreamID)
			}
			b.WriteString(line)
			b.WriteString("\n")
			if detail := strings.TrimSpace(insight.Detail); detail != "" && detail != insight.Summary {
				fmt.Fprintf(&b, "  Detail: %s\n", detail)
			}
		}
		b.WriteString("\n")
	}
	if input.Role != nil && input.Role.Name == "builder" && hasCard {
		b.WriteString("## Contract Failure Output\n")
		b.WriteString("If the stream card is contradictory or missing detail required to proceed safely, do not guess. End with:\n")
		b.WriteString("`CONTRACT_OUTCOME: contract_blocked`\n")
		b.WriteString("`CONTRACT_REASON: short explanation of the contract problem`\n\n")
	}
	if input.Role != nil && input.Role.Name == "reviewer" {
		b.WriteString("## Review Output\n")
		b.WriteString("Validate the builder against the stream card and dossier-backed constraints already in this overlay.\n")
		b.WriteString("End your final response with `REVIEW_DECISION: approve` or `REVIEW_DECISION: reject`.\n")
		b.WriteString("If you reject, add `REVIEW_FEEDBACK:` followed by the actionable issues the builder must fix.\n")
		b.WriteString("If the contract itself is missing a necessary dossier-backed requirement that was not part of the builder's contract, do not reject. End with:\n")
		b.WriteString("`CONTRACT_OUTCOME: contract_gap`\n")
		b.WriteString("`CONTRACT_REASON: short explanation of the missing requirement`\n")
		b.WriteString("`CONTRACT_STREAM_CARD:` followed by a fenced YAML replacement for this stream card only with `goal`, `acceptance_criteria`, `implementation_scope`, `proof_scope`, `hard_anchors`, and optional `seam_override_rationale`.\n")
		b.WriteString("Do not change dependencies, blocked-by edges, or file scope; Tack preserves those during local contract repair.\n\n")
		if input.RetryContext != nil && strings.TrimSpace(input.RetryContext.LastError) != "" {
			b.WriteString("When re-reviewing a retried stream, start from the previous review feedback in Retry Context. Confirm whether each prior issue is fixed. If you reject again, preserve still-unresolved concrete issues verbatim and only add newly discovered issues after them.\n\n")
		}
	}

	// 2b. Retry context (when agent is re-running after recovery)
	if input.RetryContext != nil {
		b.WriteString("## Retry Context\n")
		fmt.Fprintf(&b, "Attempt: %d/%d\n", input.RetryContext.AttemptNumber, input.RetryContext.MaxAttempts)
		fmt.Fprintf(&b, "Failure kind: %s\n\n", input.RetryContext.FailureKind)
		if input.RetryContext.LastError != "" {
			b.WriteString("Last error:\n")
			b.WriteString("```\n")
			b.WriteString(input.RetryContext.LastError)
			b.WriteString("\n```\n\n")
		}
		if input.RetryContext.HumanGuidance != "" {
			b.WriteString("Human guidance:\n")
			b.WriteString("```\n")
			b.WriteString(input.RetryContext.HumanGuidance)
			b.WriteString("\n```\n\n")
		}
		b.WriteString("Your previous code is still in the worktree. Fix the issue and continue from the existing branch state.\n\n")
	} else if input.FixContext != "" {
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
	b.WriteString("- Use tack.status() to report progress\n")
	b.WriteString("- Use tack.escalate() if you're blocked\n")
	b.WriteString("- Use tack.done() when finished\n\n")

	// 7. Delivery metadata (optional, based on requested messages)
	if input.Messages != nil && input.Messages.Any() {
		b.WriteString("## Delivery Metadata\n")
		b.WriteString("At the very end of your final response, emit exactly one line starting with `TACK_MESSAGES:` followed by compact JSON containing the requested fields below.\n")
		b.WriteString("Do not wrap it in a code fence. Keep it on a single line so Tack can parse it reliably.\n")
		b.WriteString("If your runtime supports tack.done(), include that same final `TACK_MESSAGES:` line in the summary you pass to tack.done().\n")
		if input.CommitMode == "agent" {
			b.WriteString("If you are committing manually, you may write the commit first, then emit the final TACK_MESSAGES line in your response.\n")
		}
		b.WriteString("\nRequested fields:\n")
		if input.Messages.Commit {
			b.WriteString("- `commit_message`: the exact git commit message Tack should use (subject line with optional body)\n")
		}
		if input.Messages.PR {
			b.WriteString("- `pr_title`: concise pull request title\n")
			b.WriteString("- `pr_body`: markdown pull request body summarizing the change, testing, and context\n")
		}
		b.WriteString("\nExample final line:\n")
		fields := RequestedMessageFieldNames(input.Messages)
		b.WriteString("`TACK_MESSAGES:{")
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
		b.WriteString("4. Do NOT push — Tack handles merging\n\n")
	case "none":
		// No commit instructions for analysis/scout agents.
	default: // "auto" or empty
		b.WriteString("## Commit Policy\n")
		b.WriteString("- Do NOT run git add or git commit — Tack commits your changes automatically\n\n")
	}

	// 9. Constraints
	b.WriteString("## Constraints\n")
	if hasCard {
		b.WriteString("- Execute the stream card narrowly; do not reinterpret architecture beyond the contract\n")
	}
	b.WriteString("- Do NOT modify files outside your scope\n")
	b.WriteString("- Do NOT push to git (Tack handles merging)\n")
	b.WriteString("- Do NOT install new dependencies without escalating\n")

	return b.String()
}

// planYAMLSchema is the YAML format a planner agent must output.
const planYAMLSchema = `streams:
  - title: "stream title"
    goal: "single clear execution goal"
    acceptance_criteria:
      - "concrete externally observable check"
      - "full feature surface this stream must satisfy or prove"
    implementation_scope:
      - "bounded implementation area or file glob"
    proof_scope:
      - "tests, checks, or evidence this stream must provide"
    hard_anchors:
      - instruction: "repo-specific implementation constraint"
        citation_ids:
          - "file:1"
    seam_override_rationale: "why this stream overrides a suggested dossier seam"
    file_scope:
      - "src/auth/**"
    dependencies: []  # titles of streams that must complete first
quality_gates:
  - "go test ./..."
  - "go vet ./..."`

// BuildPlannerOverlay generates a dossier-driven overlay for planner agents.
// Planners must decompose from persisted discovery context and explicitly ask
// for dossier expansion instead of exploring the repo directly.
func BuildPlannerOverlay(objective *domain.Objective, dossier *domain.Dossier, guidance string, insights []domain.ObjectiveInsight) string {
	var b strings.Builder

	b.WriteString("# Tack Agent: planner\n\n")

	b.WriteString("## Role\n")
	b.WriteString("You are a Planner agent. Decompose the objective into parallel work streams using only the persisted dossier and workflow constraints.\n")
	b.WriteString("Do not explore the codebase, inspect additional files, or do fresh repository discovery.\n\n")

	b.WriteString("## Objective\n")
	fmt.Fprintf(&b, "%s\n\n", objective.Description)

	if dossier != nil {
		if dossier.Summary != "" {
			b.WriteString("## Dossier Summary\n")
			fmt.Fprintf(&b, "%s\n\n", dossier.Summary)
		}
		if len(dossier.RepoPriors) > 0 {
			b.WriteString("## Repo Priors\n")
			for _, prior := range dossier.RepoPriors {
				fmt.Fprintf(&b, "- [%s] %s: %s\n", prior.Kind, prior.Title, prior.Detail)
			}
			b.WriteString("\n")
		}
		if len(dossier.RelevantFiles) > 0 {
			b.WriteString("## Relevant Files\n")
			for _, ref := range dossier.RelevantFiles {
				fmt.Fprintf(&b, "- `%s` - %s\n", ref.Path, ref.Reason)
			}
			b.WriteString("\n")
		}
		if len(dossier.SimilarPatterns) > 0 {
			b.WriteString("## Similar Patterns\n")
			for _, ref := range dossier.SimilarPatterns {
				fmt.Fprintf(&b, "- `%s` - %s\n", ref.Path, ref.Reason)
			}
			b.WriteString("\n")
		}
		if len(dossier.SuggestedSeams) > 0 {
			b.WriteString("## Suggested Seams\n")
			for _, seam := range dossier.SuggestedSeams {
				fmt.Fprintf(&b, "- %s - %s\n", seam.Title, seam.Reason)
				if len(seam.FilePaths) > 0 {
					fmt.Fprintf(&b, "  Files: %s\n", strings.Join(seam.FilePaths, ", "))
				}
			}
			b.WriteString("\n")
		}
		if len(dossier.Risks) > 0 {
			b.WriteString("## Risks\n")
			for _, risk := range dossier.Risks {
				fmt.Fprintf(&b, "- %s\n", risk)
			}
			b.WriteString("\n")
		}
		if len(dossier.Unknowns) > 0 {
			b.WriteString("## Unknowns\n")
			for _, unknown := range dossier.Unknowns {
				fmt.Fprintf(&b, "- %s\n", unknown)
			}
			b.WriteString("\n")
		}
		if len(dossier.Citations) > 0 {
			b.WriteString("## Dossier Citations\n")
			for _, citation := range dossier.Citations {
				fmt.Fprintf(&b, "- [%s] %s %s - %s\n", citation.ID, citation.Kind, citation.Target, citation.Detail)
			}
			b.WriteString("\n")
		}
	}

	if len(insights) > 0 {
		if patches := contractpatch.Merge(contractpatch.Compile(insights, ""), codification.AutoAppliedPatches(objective.ProjectID, objective.ID, insights)); len(patches) > 0 {
			writeContractPatches(&b, patches, "Use these derived constraints when decomposing or repairing stream cards; prefer them over rediscovering the same contract corrections.")
		}
		b.WriteString("## Objective Insights\n")
		for _, insight := range insights {
			fmt.Fprintf(&b, "- [%s/%s] %s\n", insight.Source, insight.Kind, insight.Summary)
			if detail := strings.TrimSpace(insight.Detail); detail != "" && detail != insight.Summary {
				fmt.Fprintf(&b, "  Detail: %s\n", detail)
			}
		}
		b.WriteString("\n")
	}

	if guidance != "" {
		b.WriteString("## Project Guidance\n")
		fmt.Fprintf(&b, "%s\n\n", guidance)
	}

	b.WriteString("## Instructions\n")
	b.WriteString("Produce a structured plan with:\n")
	b.WriteString("1. Streams — parallel units of work\n")
	b.WriteString("2. Acceptance criteria - concrete, externally observable checks for each stream\n")
	b.WriteString("3. Implementation scope - what code surface the stream may change\n")
	b.WriteString("4. Proof scope - what evidence the stream must provide\n")
	b.WriteString("5. Hard anchors - repo-specific constraints, each with dossier citation_ids\n")
	b.WriteString("6. File scopes - which files each stream owns (use globs)\n")
	b.WriteString("7. Dependencies - which streams must complete before others start\n")
	b.WriteString("8. Quality gates - commands to validate each stream\n\n")
	b.WriteString("Use the dossier's suggested seams when they fit. If you override them, explain why in the affected stream descriptions.\n\n")
	b.WriteString("If the dossier is insufficient for a trustworthy plan, do not guess and do not inspect the repository. Output this instead:\n")
	b.WriteString("```text\n")
	b.WriteString("PLANNER_OUTCOME: needs_dossier_expansion\n")
	b.WriteString("PLANNER_REASON: short explanation of what is missing\n")
	b.WriteString("PLANNER_FOCUS_AREAS:\n- area needing deeper discovery\n")
	b.WriteString("PLANNER_FILE_HINTS:\n- candidate/path/pattern\n")
	b.WriteString("PLANNER_QUESTIONS:\n- concrete question discovery should answer\n")
	b.WriteString("```\n\n")
	b.WriteString("For test, coverage, or docs streams, acceptance criteria must capture the full user-visible behavior they must prove or describe. Do not rely on later review to discover missing acceptance checks one-by-one.\n\n")
	b.WriteString("Output your plan as YAML in the following format:\n")
	fmt.Fprintf(&b, "```yaml\n%s\n```\n", planYAMLSchema)

	return b.String()
}

func writeContractPatches(b *strings.Builder, patches []domain.StreamCardPatch, intro string) {
	b.WriteString("## Contract Patches\n")
	b.WriteString("Derived from normalized objective-local insights, not free-form prompt history.\n")
	if strings.TrimSpace(intro) != "" {
		b.WriteString(intro)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, patch := range patches {
		line := fmt.Sprintf("- %s", patch.Instruction)
		if patch.ScopeNote != "" {
			line += fmt.Sprintf(" (%s)", patch.ScopeNote)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if patch.CandidateID != "" {
			fmt.Fprintf(b, "  Derived from: [codification_candidate/%s]\n", patch.CandidateID)
			if patch.EvidenceCount > 0 {
				fmt.Fprintf(b, "  Evidence count: %d\n", patch.EvidenceCount)
			}
		} else {
			fmt.Fprintf(b, "  Derived from: [%s/%s]\n", patch.Source, patch.Kind)
		}
		if patch.Rationale != "" && patch.Rationale != patch.Instruction {
			fmt.Fprintf(b, "  Rationale: %s\n", patch.Rationale)
		}
	}
	b.WriteString("\n")
}
