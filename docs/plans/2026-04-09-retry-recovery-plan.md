# Retry And Recovery Implementation Plan

**Date:** 2026-04-09  
**Status:** Working plan / checklist not yet fully reconciled with code

This checklist was written for the recovery redesign. Some recovery pieces have already landed in code, but this plan still remains useful as the detailed implementation backlog.

## Constraints

- Treat this as a clean-break design improvement, not a compatibility rollout.
- Keep recovery semantics centralized in code.
- Keep the public blueprint surface narrow and profile-first.
- Do not turn blueprint YAML into a policy DSL.
- Keep the blueprint engine focused on step progression, not orchestration recovery policy.

## Checklist

- [ ] Define a first-class recovery module that owns failure classification, retry policy evaluation, exhaustion handling, and attempt record construction.
- [ ] Define the internal failure taxonomy for v1 and keep it intentionally small.
- [ ] Define the bounded internal recovery action set for v1.
- [ ] Add a durable append-only `attempts` table to the DB schema.
- [ ] Add a domain model for recovery attempts with correlation fields for project, objective, run, execution, stream, step, and merge entry.
- [ ] Add a store for appending and querying attempt records.
- [ ] Thread `run_id` and any missing correlation identifiers through recovery-producing paths as needed.
- [ ] Implement a policy engine in Go that resolves profile defaults plus narrow step overrides into a concrete recovery decision.
- [ ] Add blueprint schema support for top-level retry profile selection.
- [ ] Add blueprint schema support for narrow per-step retry overrides: `max_attempts`, `on_exhausted`, and `human_guidance_mode`.
- [ ] Remove the blueprint engine from being the primary recovery brain.
- [ ] Refactor the blueprint engine so local retry bookkeeping no longer defines system-wide recovery behavior.
- [ ] Update deterministic and agent step handlers to return structured failure information suitable for classification.
- [ ] Refactor the coordinator to own recovery decisions for all retryable failures.
- [ ] Implement coordinator logic for `retry_same_step`.
- [ ] Implement coordinator logic for `rerun_previous_agent`.
- [ ] Implement coordinator logic for `restart_stream`.
- [ ] Implement coordinator logic for `retry_merge`.
- [ ] Implement coordinator logic for `ask_human_then_resume` as the default exhaustion path.
- [ ] Add explicit blocked recovery state handling distinct from normal human approval blocking.
- [ ] Refactor manual stream retry to route through the same recovery subsystem instead of a separate bespoke path.
- [ ] Replace the current free-form `fix_context` flow with a normalized recovery context payload.
- [ ] Update agent overlay generation to include attempt number, max attempts, failure kind, last error summary, prior attempt history, and optional human guidance.
- [ ] Ensure recovery attempts reuse or restart sandboxes according to the selected recovery action.
- [ ] Classify transient agent runtime failures separately from agent output failures.
- [ ] Make quality gate failures participate in the unified attempt ledger and policy engine.
- [ ] Make reviewer-driven repair loops participate in the same recovery model.
- [ ] Make merge conflicts participate in the same recovery model.
- [ ] Make post-merge gate failures participate in the same recovery model, including merge revert plus repair resumption.
- [ ] Ensure recovery attempt counts and decisions are visible in operator observability output.
- [ ] Update `watch` and related live views to show recovery attempt number, budget, and blocked recovery states.
- [ ] Update logs and stored observability records to reflect the new recovery model consistently.
- [ ] Add unit tests for failure classification and policy evaluation.
- [ ] Add unit tests for profile resolution and per-step override precedence.
- [ ] Add unit tests for exhaustion behavior and human-resume decision making.
- [ ] Add coordinator tests for same-step retry, previous-agent rerun, stream restart, merge retry, and human-guided resume.
- [ ] Add integration tests for repeated quality gate failures with eventual recovery.
- [ ] Add integration tests for reviewer rejection followed by repaired builder rerun.
- [ ] Add integration tests for transient agent runtime failures that recover in place.
- [ ] Add integration tests for merge conflict recovery with bounded attempts and human guidance.
- [ ] Add integration tests for post-merge gate failure recovery with revert and repair.
- [ ] Add recovery persistence and restart tests so blocked or in-flight recovery survives daemon restart.
- [ ] Run `gofmt` and `go test ./...`.
