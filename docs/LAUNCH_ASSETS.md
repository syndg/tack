# Tack Launch Assets

## GitHub Repository Description

Deterministic harness for agentic code execution: plan, parallelize, gate, recover, merge, PR.

## Pinned Launch Issue Draft

Title: `Public alpha: try Tack and report first-run friction`

Tack is now in public alpha.

Tack turns one natural-language objective into a bounded agentic coding workflow: discovery, planning, human approval, parallel streams, quality gates, review, recovery, merge, and PR creation.

What works best today:

- Local git worktrees
- Pi runtime
- Small, scoped objectives with clear acceptance criteria
- Existing projects with deterministic test/lint/typecheck commands

What is rough:

- First-run setup still depends on provider credentials, git credentials, and project setup commands
- Local worktrees are not OS sandboxing; agents run as your user
- Claude Code is minimal compatibility, not the primary path
- Docker, E2B, and semantic AI merge are planned, not launch promises

Good first things to try:

```bash
tack plan "Add a health check endpoint at GET /health that returns 200 OK"
tack plan "Fix the typo in README.md and run the existing docs checks"
tack plan "Add pagination to all list endpoints with tests"
```

Useful reports include:

- OS and Tack version
- Runtime, provider, and model
- Sandbox provider
- Objective text
- What happened vs what you expected
- Short `tack watch --summary` output with secrets redacted

Please prioritize first-run bugs, docs gaps, unclear errors, and cases where Tack makes unsafe or surprising choices.

## Short Launch Post

I’m open-sourcing Tack.

Tack is a deterministic harness around coding agents: one objective in, reviewed/tested/merged PR out.

Instead of one agent wandering around your repo, Tack runs discovery, decomposes work into scoped parallel streams, runs agents in isolated worktrees, gates changes, reviews output, recovers from failures, merges, and opens a PR.

It’s public alpha. Best path today: local worktrees + Pi runtime + small scoped objectives.

Repo: https://github.com/syndg/tack

## Longer Technical Post

Title: `Tack: deterministic harness for agentic code execution`

Most coding-agent workflows still feel like handing a repo to one agent and hoping the final diff is coherent.

Tack takes a different shape: the model is the worker, but the harness owns the workflow.

You submit one objective. Tack runs discovery first, creates a cited context dossier, decomposes the objective into parallel streams with file scopes and dependencies, waits for human approval, executes each stream in an isolated worktree, runs deterministic quality gates, reviews the result, retries recoverable failures, merges streams in dependency order, runs post-merge gates, and creates a PR.

The deterministic parts are the product: blueprints, file scopes, scoped rules, quality gates, merge ordering, retry budgets, recovery policy, and observability. The agent runtime is replaceable.

It is public alpha. The best-supported path is local git worktrees with the Pi runtime. Local mode is not OS sandboxing; agents run as your user. Daytona is available for VM-level isolation.

Two current benchmarks are against real lazygit features with frozen plan shapes and deterministic validation. One completed three streams with zero recovery events. Another completed three streams with five automatic recoveries and zero human intervention.

I’m looking for first-run feedback: install friction, credential setup problems, unclear errors, unsafe defaults, bad plans, and docs gaps.

Repo: https://github.com/syndg/tack

## Copy-Paste Objectives

```bash
tack plan "Add a health check endpoint at GET /health that returns 200 OK"
tack plan "Fix the race condition in src/services/websocket.ts and add a regression test"
tack plan "Add pagination to all list endpoints with page and per_page query params"
tack plan "Extract shared validation logic from src/routes/users.ts and src/routes/expenses.ts without changing behavior"
tack plan "Add a PATCH /expenses/:id endpoint with partial update validation and tests"
```

## FAQ

### Why not just use Claude Code, Codex, or another agent CLI directly?

Direct agent CLIs are useful workers. Tack is the harness around workers: discovery, planning, file scopes, gates, review, recovery, merge ordering, observability, and PR creation.

### Why a local daemon?

The daemon coordinates long-running work across CLI commands, agent processes, sandboxes, logs, mail, recovery state, and merge queues. You can start work, inspect it from another terminal, and resume after failures.

### How safe is local mode?

Local mode uses git worktrees and runs agents as your user. It is not container or VM isolation. Use it only on repos you trust locally. Use Daytona when you need VM-level isolation.

### Where do credentials live?

Provider, git, and sandbox credentials live in `~/.config/tack/credentials.yaml`, not in repo config. Project config in `.tack/config.yaml` is intentionally committable and should not contain secrets.

### Why blueprints?

Blueprints make the workflow explicit. You can inspect or override which steps are agentic, which are deterministic, how retries work, and what happens on failure.

### Is Tack production ready?

No. Tack is public alpha. Use it for scoped objectives, expect rough edges, and report first-run friction.

## Maintainer Response Snippets

### Install Failure

Thanks for trying Tack. Please share OS/arch, install method, `tack version` output if available, and the exact command/output. If this was the install script, also include whether `curl`, `tar`, and `/usr/local/bin` are available/writable.

### Daemon Unreachable

The daemon needs to run separately. Start `tack daemon` in one terminal, then run project commands from another terminal inside your repo. If it still fails, share `tack status`, the daemon output, and whether port `127.0.0.1:9800` is already in use.

### Credential Setup

Please run `tack auth list` and `tack auth test <provider>` and share redacted output. Do not paste API keys, OAuth tokens, daemon tokens, or `credentials.yaml` contents.

### Provider Rate Limit

This is usually provider-side throttling. Share provider/model, objective size, and whether multiple streams were running. You can reduce `agents.max_concurrent` in `.tack/config.yaml` while we improve default backoff behavior.

### Quality Gate Failure

Please share the objective, stream file scope from `tack show <plan-id>`, and the redacted gate output from `tack watch --verbose --stream <stream-id>`. Tack should only hold a stream responsible for failures in its scoped files.

### Merge Conflict

Please share `tack merge`, `tack merge diff <stream-id>`, and whether the conflicting streams touched overlapping files. If retry fails, a follow-up objective may be cleaner than forcing a manual merge.
