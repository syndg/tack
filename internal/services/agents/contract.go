package agents

import "strings"

const (
	contractOutcomePrefix    = "CONTRACT_OUTCOME:"
	contractReasonPrefix     = "CONTRACT_REASON:"
	contractStreamCardPrefix = "CONTRACT_STREAM_CARD:"

	ContractOutcomeBlocked = "contract_blocked"
	ContractOutcomeGap     = "contract_gap"
)

type ContractOutcome struct {
	Kind           string
	Reason         string
	StreamCardYAML string
}

func ParseContractOutcome(summary string) (ContractOutcome, bool) {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ContractOutcome{}, false
	}

	lines := strings.Split(trimmed, "\n")
	outcome := ContractOutcome{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, contractOutcomePrefix):
			outcome.Kind = strings.ToLower(strings.TrimSpace(line[len(contractOutcomePrefix):]))
		case strings.HasPrefix(upper, contractReasonPrefix):
			outcome.Reason = strings.TrimSpace(line[len(contractReasonPrefix):])
		case strings.HasPrefix(upper, contractStreamCardPrefix):
			card := strings.TrimSpace(line[len(contractStreamCardPrefix):])
			if card == "" && i+1 < len(lines) {
				card = strings.Join(lines[i+1:], "\n")
				i = len(lines)
			}
			outcome.StreamCardYAML = stripOptionalYAMLFence(card)
		}
	}

	switch outcome.Kind {
	case ContractOutcomeBlocked, ContractOutcomeGap:
		if strings.TrimSpace(outcome.Reason) == "" {
			outcome.Reason = trimmed
		}
		return outcome, true
	default:
		return ContractOutcome{}, false
	}
}

func stripOptionalYAMLFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		return trimmed
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
