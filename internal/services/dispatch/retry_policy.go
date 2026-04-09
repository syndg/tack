package dispatch

import (
	"strings"

	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/recovery"
)

func resolveExecutionRetryConfig(engine *blueprint.Engine, exec *blueprint.Execution) recovery.Config {
	if engine == nil || exec == nil {
		return recovery.Config{}
	}
	bp, ok := engine.GetBlueprint(exec.BlueprintID)
	if !ok || bp == nil || bp.Retry == nil {
		return recovery.Config{}
	}
	return recovery.Config{
		Profile:            bp.Retry.Profile,
		DefaultMaxAttempts: bp.Retry.DefaultMaxAttempts,
		DefaultOnExhausted: bp.Retry.DefaultOnExhausted,
	}
}

func resolveStepRetryOverride(step *blueprint.Step) recovery.StepOverride {
	if step == nil {
		return recovery.StepOverride{}
	}
	override := recovery.StepOverride{}
	if step.Retry != nil {
		override.MaxAttempts = step.Retry.MaxAttempts
		override.OnExhausted = step.Retry.OnExhausted
		override.HumanGuidanceMode = step.Retry.HumanGuidanceMode
	}
	if override.MaxAttempts == 0 && step.MaxFixIterations > 0 {
		override.MaxAttempts = step.MaxFixIterations
	}
	return override
}

func classifySpawnFailure(message string) domain.FailureKind {
	msg := strings.ToLower(message)
	switch {
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return domain.FailureProviderRateLimit
	case strings.Contains(msg, "sandbox"), strings.Contains(msg, "provision"), strings.Contains(msg, "daytona"):
		return domain.FailureSandbox
	default:
		return domain.FailureAgentRuntimeTransient
	}
}

func classifyAgentFailure(message string) domain.FailureKind {
	msg := strings.ToLower(message)
	switch {
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return domain.FailureProviderRateLimit
	case strings.Contains(msg, "sandbox"), strings.Contains(msg, "provision"), strings.Contains(msg, "daytona"):
		return domain.FailureSandbox
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"), strings.Contains(msg, "timed out"):
		return domain.FailureAgentRuntimeTransient
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "broken pipe"):
		return domain.FailureAgentRuntimeTransient
	case strings.Contains(msg, "temporarily unavailable"), strings.Contains(msg, "unavailable"), strings.Contains(msg, "eof"):
		return domain.FailureAgentRuntimeTransient
	case strings.Contains(msg, "killed"):
		return domain.FailureAgentRuntimeTransient
	case strings.Contains(msg, "exit code"), strings.Contains(msg, "non-zero exit"):
		return domain.FailureAgentOutput
	default:
		return domain.FailureAgentOutput
	}
}
