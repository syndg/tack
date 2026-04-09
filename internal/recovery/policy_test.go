package recovery

import (
	"testing"

	"github.com/syndg/tack/internal/domain"
)

func TestResolvePolicyDefaults(t *testing.T) {
	policy := ResolvePolicy(Config{}, StepOverride{})
	if policy.Profile != ProfileBalanced {
		t.Fatalf("profile = %q, want %q", policy.Profile, ProfileBalanced)
	}
	if policy.MaxAttempts != 4 {
		t.Fatalf("max_attempts = %d, want 4", policy.MaxAttempts)
	}
	if policy.OnExhausted != domain.ExhaustionAskHuman {
		t.Fatalf("on_exhausted = %q, want %q", policy.OnExhausted, domain.ExhaustionAskHuman)
	}
}

func TestDecideUsesFailureKindDefaults(t *testing.T) {
	policy := ResolvePolicy(Config{Profile: ProfileSelfHealing}, StepOverride{})
	decision := Decide(domain.FailureQualityGate, 1, policy)
	if decision.Action != domain.RecoveryActionRerunPreviousAgent {
		t.Fatalf("action = %q, want %q", decision.Action, domain.RecoveryActionRerunPreviousAgent)
	}
}

func TestDecideUsesExhaustionAction(t *testing.T) {
	policy := ResolvePolicy(Config{}, StepOverride{MaxAttempts: 2, OnExhausted: domain.ExhaustionFail})
	decision := Decide(domain.FailureMergeConflict, 2, policy)
	if decision.Action != domain.RecoveryActionFailTerminal {
		t.Fatalf("action = %q, want %q", decision.Action, domain.RecoveryActionFailTerminal)
	}
}
