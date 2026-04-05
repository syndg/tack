# Operator Observability Implementation Plan

## Constraints

- Build a deep module with a small public boundary.
- Treat the canonical operator record as the new source model in code.
- Do not add backward-compatibility readers, backfills, or migration guards for old log formats.
- Keep the current persistence split: full-fidelity JSONL, projected subset in DB events.

## Checklist

- [ ] Add a deep `internal/observability` module that owns canonical record creation, normalization, projection, and persistence.
- [ ] Define the canonical `OperatorRecord` envelope with fixed top-level correlation fields: `ts`, `project_id`, `objective_id`, `stream_id`, `agent_id`, `role`, `kind`, `summary`, `status`, and `details`.
- [ ] Define the initial operator event taxonomy for v1, covering both stream-centric milestones and fine-grained agent activity.
- [ ] Keep the module interface small so callers report domain facts instead of building records or choosing storage behavior themselves.
- [ ] Implement full-fidelity JSONL writing for canonical records inside the observability module.
- [ ] Replace the current narrow per-agent activity log shape with canonical record output only.
- [ ] Implement DB event projection inside the observability module for milestones plus notable activity only.
- [ ] Define and encode the projection policy for notable activity so high-value failures and outcomes reach `tack watch` without flooding the event stream.
- [ ] Refactor `internal/services/dispatch/agent_tracker.go` to emit canonical activity facts through the observability module.
- [ ] Ensure agent-originated records always carry full correlation metadata from the active session context.
- [ ] Refactor agent lifecycle emission in dispatch paths so spawn, completion, failure, and kill events also flow through the observability module.
- [ ] Refactor stream and execution milestone producers in scheduler, runs, planner, merge, and escalation paths to use the observability module instead of ad hoc event payload construction.
- [ ] Remove direct caller responsibility for JSON payload shaping where the observability module now owns the canonical summary and `details` structure.
- [ ] Keep `domain.Event` persistence and SSE transport, but make them projections of canonical operator records rather than first-class hand-built payloads.
- [ ] Update `cmd/tack/logs.go` to read and render canonical JSONL records only.
- [ ] Update `cmd/tack/watch.go` to render projected DB/SSE events as the live operator timeline for the same canonical model.
- [ ] Align `watch` and `logs` formatting inputs so equivalent operator facts produce consistent output across both commands.
- [ ] Add focused boundary tests for the observability module: canonical envelope completion, summary generation, JSONL persistence, and DB projection.
- [ ] Add scenario tests that cover a full stream lifecycle from agent spawn through merge and assert both replay fidelity and projected timeline correctness.
- [ ] Add tests that verify non-notable fine-grained activity stays out of DB events while still appearing in JSONL replay.
- [ ] Add tests that verify all canonical records include correlation fields, even when some values are empty.
- [ ] Run `gofmt` and `go test ./...`.
