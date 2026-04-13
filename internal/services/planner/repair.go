package planner

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
	"gopkg.in/yaml.v3"
)

type rawStreamCardRepair struct {
	Goal                  string      `yaml:"goal"`
	AcceptanceCriteria    []string    `yaml:"acceptance_criteria"`
	ImplementationScope   []string    `yaml:"implementation_scope"`
	ProofScope            []string    `yaml:"proof_scope"`
	HardAnchors           []RawAnchor `yaml:"hard_anchors"`
	SeamOverrideRationale string      `yaml:"seam_override_rationale"`
}

func CompileStreamCardRepair(rawYAML string, stream domain.Stream, dossier *domain.Dossier) (domain.StreamCard, error) {
	rawYAML = strings.TrimSpace(rawYAML)
	if rawYAML == "" {
		return domain.StreamCard{}, fmt.Errorf("contract gap repair missing stream card YAML")
	}

	var raw rawStreamCardRepair
	if err := yaml.Unmarshal([]byte(rawYAML), &raw); err != nil {
		return domain.StreamCard{}, fmt.Errorf("parsing repaired stream card: %w", err)
	}

	card := stream.EffectiveCard()
	if goal := strings.TrimSpace(raw.Goal); goal != "" {
		card.Goal = goal
	}
	if raw.AcceptanceCriteria != nil {
		card.AcceptanceCriteria = trimRepairList(raw.AcceptanceCriteria)
	}
	if raw.ImplementationScope != nil {
		card.ImplementationScope = trimRepairList(raw.ImplementationScope)
	}
	if raw.ProofScope != nil {
		card.ProofScope = trimRepairList(raw.ProofScope)
	}
	if raw.HardAnchors != nil {
		hardAnchors, err := compileHardAnchors(stream.Title, raw.HardAnchors, dossier)
		if err != nil {
			return domain.StreamCard{}, err
		}
		card.HardAnchors = hardAnchors
	}
	if rationale := strings.TrimSpace(raw.SeamOverrideRationale); rationale != "" {
		card.SeamOverrideRationale = rationale
	}
	if strings.TrimSpace(card.Goal) == "" {
		return domain.StreamCard{}, fmt.Errorf("repaired stream card missing goal")
	}

	return card, nil
}

func trimRepairList(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{}
	}
	return out
}
