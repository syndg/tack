# Retry And Recovery Design

## Goal

Make Tack self-healing by default.

When work fails, the system should classify the failure, record the attempt durably, choose the correct recovery action, and continue automatically whenever policy allows. When recovery budget is exhausted, the default behavior should be to ask a human for guidance and then resume from the right recovery point.

This is an orchestration-level error-handling system, not a collection of local `if err != nil` branches.

## Current State

Tack already has partial retry behavior, but it is fragmented:

- blueprint step retries live in the blueprint engine
- stream-local quality gate failures can loop back to the builder step
- failed streams can be retried manually through the runs boundary
- merge and post-merge failures are mostly terminal

This creates three problems:

- retry behavior is inconsistent across failure types
- attempt counts are local bookkeeping instead of durable system state
- human guidance is not part of one canonical recovery loop

## Recommendation

Use a hybrid retry model with coordinator-owned recovery.

- code owns recovery semantics, failure classification, and default policy
- blueprints expose only a small override surface
- the coordinator is the recovery brain
- the blueprint engine remains a step machine, not the recovery brain
- all recovery attempts are stored in an append-only attempt ledger

This keeps Tack aligned with its product thesis: bounded, deterministic workflows with a strong harness around agentic steps.

## Design Principles

- Keep the public configuration surface narrow.
- Prefer profiles over raw policy programming.
- Keep recovery semantics centralized in code.
- Record every recovery hop durably and append-only.
- Make human guidance part of the normal healing loop.
- Treat retries as first-class orchestration, not handler-local behavior.
- Do not preserve the current fragmented retry model as a compatibility target.

## Control Plane

The coordinator owns recovery decisions.

The blueprint engine should only do three things:

- advance the current execution step
- record local step state
- stop on terminal, blocked, or completed execution state

The engine should not decide system-wide recovery behavior for merge failures, human guidance, or cross-step attempt budgets. Existing local retry fields such as `RetryCount`, `OnFail`, and `MaxFixIterations` are useful scaffolding, but they are too narrow to remain the primary recovery model.

The new control flow is:

1. a step handler or merge handler returns a structured failure
2. the coordinator classifies the failure into a bounded internal taxonomy
3. the coordinator appends an attempt record
4. the coordinator consults the policy engine
5. the coordinator chooses the next recovery action
6. the coordinator rewrites execution state and resumes from the correct point

## Policy Model

The public API should be profile-first.

Example blueprint surface:

```yaml
retry:
  profile: self_healing

steps:
  - id: build
    type: agent
    role: builder
    next: lint

  - id: lint
    type: deterministic
    action: run_quality_gates
    retry:
      max_attempts: 6
      on_exhausted: ask_human
    next: review
```

Supported top-level config:

- `profile`
- optional `default_max_attempts`
- optional `default_on_exhausted`

Supported per-step overrides:

- `max_attempts`
- `on_exhausted`
- `human_guidance_mode`

Profiles are the main UX:

- `strict`: low retry budgets, fast escalation, optimized for predictability
- `balanced`: retries transient failures, limited fix loops, asks human on exhaustion
- `self_healing`: higher budgets, stronger repair loops, prefers human guidance before terminal failure

Failure taxonomy, recovery actions, and routing rules remain code-defined.

## Failure Taxonomy

Keep the internal taxonomy small and explicit.

Initial failure kinds:

- `agent_runtime_transient`
- `agent_output_failure`
- `quality_gate_failure`
- `review_rejection`
- `sandbox_failure`
- `provider_rate_limit`
- `merge_conflict`
- `post_merge_gate_failure`
- `deterministic_step_failure`

Each failure kind maps to a bounded internal recovery action set:

- `retry_same_step`
- `rerun_previous_agent`
- `restart_stream`
- `retry_merge`
- `ask_human_then_resume`
- `fail_terminal`

## Recovery Flow

Recovery should be uniform even when policies differ by failure kind.

Example behaviors:

- transient agent runtime failures retry the same step in place
- quality gate failures rerun the responsible agent with normalized retry context
- review rejection reruns the builder with reviewer feedback attached
- merge conflicts retry merge with bounded attempts, then ask the human
- post-merge gate failures revert the merge, attach the failure to the stream, and resume from the repair point

The default exhaustion behavior is `ask_human_then_resume`.

That means:

1. the run enters a blocked recovery state
2. Tack emits a human-visible notification with attempt history and summarized context
3. the human can provide guidance
4. the coordinator injects that guidance into the next recovery attempt
5. the system resumes from the correct recovery point

This keeps the human inside the healing loop instead of treating them as a separate emergency escape hatch.

## Attempt Ledger

Add a durable append-only attempt ledger as the source of truth for recovery state.

Each record should capture:

- `id`
- `project_id`
- `objective_id`
- `run_id`
- `execution_id`
- `stream_id`
- `step_id`
- `merge_entry_id`
- `attempt_number`
- `max_attempts`
- `failure_kind`
- `action`
- `status`
- `error_summary`
- `fix_context`
- `human_guidance`
- `triggered_by_attempt_id`
- `created_at`

This ledger should be append-only. Recovery history must be inspectable without reconstructing state from mutable counters.

Current engine-local counters can remain as internal execution mechanics if helpful, but they should no longer be the system source of truth.

## Execution State

The coordinator should become the owner of recovery state transitions.

Key execution states should distinguish normal approval blocking from recovery blocking. Add explicit recovery-aware states where needed so operators can tell the difference between:

- waiting for plan approval
- waiting for human recovery guidance
- running a normal stream attempt
- running a recovery attempt

Sub-execution restart and reroute should be explicit operations performed by the coordinator, not implicit side effects hidden in step handlers.

## Agent Retry Context

Agents should receive normalized retry context, not just a raw failure blob.

The overlay for a recovery attempt should include:

- current attempt number and max attempts
- failure kind
- last error summary
- compact prior attempt history
- human guidance, if present
- clear instruction that existing worktree state is still present unless the recovery action restarted the stream

This replaces the current single-string `fix_context` model with structured recovery context while preserving the same product idea.

## Merge Recovery

Merge and post-merge failures should participate in the same recovery system.

For merge conflict:

- append an attempt record
- retry merge according to profile budget
- on exhaustion, ask the human and resume

For post-merge gate failure:

- revert the merge
- append an attempt record
- attach failure context to the affected stream
- resume from the configured repair point

This removes the current split where stream-local gates can self-heal but merge-time failures are mostly terminal.

## Observability

Recovery should be visible in `watch`, logs, SSE, and stored operator history.

Examples:

- `stream payments-api: quality_gate_failure attempt 3/6; rerunning builder`
- `merge retry attempt 2/2 exhausted; waiting for human guidance`
- `resumed stream auth after human guidance`

The operator-facing timeline should reflect the attempt ledger and coordinator decisions, not ad hoc retry messages assembled by individual handlers.

## Non-Goals

- expose the full failure taxonomy as user-authored YAML policy
- turn blueprints into a policy DSL
- preserve the current retry split as a compatibility contract
- solve every future failure kind in v1

## Testing

Focus on orchestration-level correctness.

Unit tests:

- failure classification
- policy selection by profile and override
- exhaustion behavior
- attempt ledger append semantics

Coordinator and engine boundary tests:

- retry same step
- rerun previous agent
- restart stream sub-execution
- ask human then resume
- correct attempt numbering across repeated failures

Integration tests:

- repeated quality gate failures with eventual success
- reviewer rejection followed by repaired builder run
- transient runtime failures that recover in place
- merge conflict with bounded retries and human resume
- post-merge gate failure that reverts and resumes repair
- daemon restart during blocked recovery or in-flight recovery

Operator UX tests:

- `watch` output shows attempt counts and blocked recovery states
- logs and SSE reflect the same recovery model

## Recommendation Summary

Implement one recovery subsystem with these properties:

- coordinator-owned recovery
- hybrid policy model with profile-first UX
- append-only attempt ledger
- narrow blueprint overrides
- human guidance as a normal recovery action
- merge and post-merge recovery included from the start

That gives Tack a coherent self-healing architecture instead of a collection of isolated retry loops.
