# Ledger: E2E Test Project for Tack Core Loop

**Date:** 2026-03-13
**Status:** Approved
**Context:** Need a small, real project to test tack's Phases 1-5 end-to-end with multiple agents.

---

## Project

**Ledger** — an expense & subscription tracker REST API. Hono on Bun, SQLite via `bun:sqlite`, TypeScript. Located at `/Volumes/External/Coding/ledger`.

Exists solely to exercise tack's core loop: planning → approval → dispatch → sub-execution (build → lint → review → merge) → complete.

## Scaffold (pre-built)

The repo ships with a working skeleton that agents build on top of:

```
ledger/
├── package.json          # hono, typescript, @types/bun
├── tsconfig.json         # strict, noEmit
├── src/
│   ├── index.ts          # Hono app, health endpoint, route mount points
│   ├── db.ts             # SQLite init, expenses + subscriptions tables
│   ├── types.ts          # Expense, Subscription, CategorySummary, MonthlySummary
│   └── routes/.gitkeep   # agents populate this
└── tests/
    └── health.test.ts    # basic health check test
```

**Quality gates:** `bunx tsc --noEmit && bun test`

## Objective Prompt

> Build an expense tracker API with: (1) expense CRUD — create, list, filter by category/date range, delete; (2) recurring subscriptions — create, list, cancel, with billing cycle tracking; (3) a summary endpoint that returns spending totals by category and month, including projected subscription costs.

This gives the planner three obvious seams while leaving decomposition up to it. The summary endpoint naturally depends on both expenses and subscriptions.

## Expected Stream Decomposition

The planner should produce something like:

| Stream | File Scope | Dependencies |
|--------|-----------|-------------|
| Expense CRUD | `src/routes/expenses.ts`, `tests/expenses.test.ts` | none |
| Subscription management | `src/routes/subscriptions.ts`, `tests/subscriptions.test.ts` | none |
| Summary & reporting | `src/routes/summary.ts`, `tests/summary.test.ts` | streams 1, 2 |

Streams 1 and 2 run in parallel (independent). Stream 3 cascades after both complete.

## What This Tests

| Tack capability | How it's exercised |
|----------------|-------------------|
| Planner agent | Decomposes objective into streams with file scopes and dependencies |
| Human approval gate | User reviews and approves the plan |
| Stream dispatch | Scheduler identifies ready streams, respects dependency graph |
| Parallel sub-execution | Streams 1 and 2 run concurrently |
| Dependency cascade | Stream 3 starts only after 1 and 2 complete |
| Builder agent | Writes route handlers, types, and tests in worktree sandbox |
| Quality gates | `bunx tsc --noEmit && bun test` runs after each build |
| Fix-loop | If typecheck or tests fail, builder re-runs with error context |
| Reviewer agent | Reviews builder's changes |
| Merge processor | Sequences merge_ready streams into main |
| Objective completion | All streams merged → objective completed |

## What This Does NOT Test

- Scout agents (no file discovery needed — scaffold is small)
- Hotfix blueprint
- Daytona sandboxes (local worktrees only)
- AI conflict resolution (tier 3 merge — streams have isolated file scopes)
- Escalation to human (would need a deliberate failure)

## Tack Configuration

The daemon needs to run against the ledger repo. Key config overrides:

```yaml
sandbox:
  provider: local

quality_gates:
  - "bunx tsc --noEmit"
  - "bun test"
```

The local sandbox provider creates worktrees from the ledger repo's git history.

## Running the Test

```bash
# 1. Start daemon pointed at ledger repo
cd /Volumes/External/Coding/ledger
tack daemon --config tack.yaml

# 2. Create objective
tack objective create "Build an expense tracker API with: (1) expense CRUD — create, list, filter by category/date range, delete; (2) recurring subscriptions — create, list, cancel, with billing cycle tracking; (3) a summary endpoint that returns spending totals by category and month, including projected subscription costs."

# 3. Wait for planner → review plan
tack plan show <plan-id>

# 4. Approve
tack plan approve <plan-id>

# 5. Watch execution
tack status <objective-id>

# 6. Check result
tack objective get <objective-id>
```

## Success Criteria

1. Planner produces a multi-stream plan with correct dependencies
2. Independent streams execute in parallel
3. Dependent stream waits and starts via cascade
4. Quality gates run and pass (or fix-loop corrects failures)
5. All streams reach merge_ready
6. Merge processor sequences them into main
7. Objective reaches `completed`
8. The ledger API actually works: `bun run src/index.ts` serves working endpoints
