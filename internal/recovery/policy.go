package recovery

import "github.com/syndg/tack/internal/domain"

// Profile is the user-facing retry posture.
type Profile string

const (
	ProfileStrict      Profile = "strict"
	ProfileBalanced    Profile = "balanced"
	ProfileSelfHealing Profile = "self_healing"
)

// Config is the profile-driven retry configuration surface used by the policy engine.
type Config struct {
	Profile            Profile
	DefaultMaxAttempts int
	DefaultOnExhausted domain.ExhaustionMode
	HumanGuidanceMode  string
}

// StepOverride is the narrow per-step override surface.
type StepOverride struct {
	MaxAttempts       int
	OnExhausted       domain.ExhaustionMode
	HumanGuidanceMode string
}

// ResolvedPolicy is the concrete retry policy after profile defaults and step overrides are merged.
type ResolvedPolicy struct {
	Profile           Profile
	MaxAttempts       int
	OnExhausted       domain.ExhaustionMode
	HumanGuidanceMode string
}

// Decision is the next recovery action chosen by the policy engine.
type Decision struct {
	Action domain.RecoveryAction
	Policy ResolvedPolicy
	Reason string
}

// ResolvePolicy merges profile defaults with a narrow step override.
func ResolvePolicy(cfg Config, override StepOverride) ResolvedPolicy {
	profile := cfg.Profile
	if profile == "" {
		profile = ProfileBalanced
	}

	maxAttempts := cfg.DefaultMaxAttempts
	if maxAttempts <= 0 {
		switch profile {
		case ProfileStrict:
			maxAttempts = 2
		case ProfileSelfHealing:
			maxAttempts = 6
		default:
			maxAttempts = 4
		}
	}
	if override.MaxAttempts > 0 {
		maxAttempts = override.MaxAttempts
	}

	onExhausted := cfg.DefaultOnExhausted
	if onExhausted == "" {
		onExhausted = domain.ExhaustionAskHuman
	}
	if override.OnExhausted != "" {
		onExhausted = override.OnExhausted
	}

	humanGuidanceMode := cfg.HumanGuidanceMode
	if humanGuidanceMode == "" {
		humanGuidanceMode = "append"
	}
	if override.HumanGuidanceMode != "" {
		humanGuidanceMode = override.HumanGuidanceMode
	}

	return ResolvedPolicy{
		Profile:           profile,
		MaxAttempts:       maxAttempts,
		OnExhausted:       onExhausted,
		HumanGuidanceMode: humanGuidanceMode,
	}
}

// Decide returns the next recovery action for a failure kind and attempt count.
func Decide(kind domain.FailureKind, attemptNumber int, policy ResolvedPolicy) Decision {
	if policy.MaxAttempts > 0 && attemptNumber >= policy.MaxAttempts {
		return Decision{
			Action: actionForExhaustion(policy.OnExhausted),
			Policy: policy,
			Reason: "attempt budget exhausted",
		}
	}

	return Decision{
		Action: defaultAction(kind),
		Policy: policy,
		Reason: "policy default",
	}
}

func actionForExhaustion(mode domain.ExhaustionMode) domain.RecoveryAction {
	switch mode {
	case domain.ExhaustionFail:
		return domain.RecoveryActionFailTerminal
	case domain.ExhaustionEscalate:
		return domain.RecoveryActionAskHumanThenResume
	default:
		return domain.RecoveryActionAskHumanThenResume
	}
}

func defaultAction(kind domain.FailureKind) domain.RecoveryAction {
	switch kind {
	case domain.FailureAgentRuntimeTransient, domain.FailureSandbox, domain.FailureProviderRateLimit:
		return domain.RecoveryActionRetrySameStep
	case domain.FailureQualityGate, domain.FailureReviewRejection, domain.FailureContractGap:
		return domain.RecoveryActionRerunPreviousAgent
	case domain.FailureContractBlocked:
		return domain.RecoveryActionAskHumanThenResume
	case domain.FailureMergeConflict:
		return domain.RecoveryActionRetryMerge
	case domain.FailurePostMergeGate:
		return domain.RecoveryActionRestartStream
	default:
		return domain.RecoveryActionAskHumanThenResume
	}
}
