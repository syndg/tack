# Sandbox Reuse: One Sandbox Per Stream

**Date:** 2026-03-17
**Status:** Approved
**Scope:** Sandbox lifecycle, naming, branch strategy, merger reuse

---

## Problem

Sandbox creation is the most expensive operation in the Daytona pipeline (~30s per sandbox: provision + clone + install Pi + bun install). The previous model created a new sandbox per agent step, leading to:

- **Resource exhaustion**: 3-stream plan creates up to 10 sandboxes (planner + 3 scouts + 3 builders + 3 reviewers + merger). Exceeds Daytona Tier 1 limit (10 vCPUs).
- **Wrong naming**: Sandboxes named after the first agent role (`deck-{obj}-scout-{id}`). Dashboard shows 3 "scouts" when really it's 3 streams doing scout→build→review.
- **Branch collision**: With sandbox reuse, all agents in a stream inherit the scout's branch name. Merger sees 3 identical branch names instead of 3 distinct stream branches.
- **Slow**: Unnecessary clone + install overhead on every agent transition.

## Design

### One sandbox per stream

Each stream gets exactly one sandbox. All agents within that stream (scout, builder, reviewer) share it. The sandbox is created when the first agent in the stream spawns, and reused by subsequent agents.

**Sandbox naming**: `deck-{obj[:8]}-{streamSlug}`

Where `streamSlug` is a URL-safe, truncated version of the stream title:
- "CORS Middleware" → `cors-middleware`
- "Health Check DB Verification" → `health-check-db`
- Truncated to 30 chars, lowercase, alphanumeric + hyphens only

**Branch naming**: `deck/{obj[:8]}/{streamSlug}`

One branch per stream, created at sandbox creation time. All agents in the stream work on this branch. The scout explores on it, the builder commits to it, the reviewer reviews it.

**Sandbox labels**: Same as today but `deck.role` removed (sandbox isn't role-specific):
```
deck.objective: {objectiveID}
deck.stream: {streamID}
```

### Planner sandbox

The planner is the only agent that doesn't belong to a stream. It gets its own sandbox named `deck-{obj[:8]}-planner`. This sandbox is deleted immediately after the plan is created (already implemented).

### Merger reuses a stream sandbox

When all streams reach `merge_ready`, the merger:

1. Picks any stream sandbox (first available)
2. Runs `git checkout {baseBranch}` (separate command, not `&&`)
3. Runs `git reset --hard origin/{baseBranch}` (separate command)
4. Runs `git fetch origin` to get all stream branches
5. Merges stream branches sequentially: stream-1, then stream-2 on top, then stream-3
6. Runs quality gates on the merged result
7. If all pass, pushes and creates PR

No new sandbox is created for the merger. This avoids Daytona CPU limits entirely.

### Spawner changes

The spawner's `Spawn` method changes:

1. **Stream agents** (scout/builder/reviewer): Always look up existing sandbox by `deck.stream` label first. Only create if none exists (first agent in stream).
2. **Planner agents**: Create new sandbox (no stream context). Delete after completion.
3. **Sandbox naming**: Derive from stream title, not role.
4. **Branch naming**: Derive from stream title, not role + UUID.

```go
// Before (per-agent):
name = fmt.Sprintf("deck-%s-%s-%s", objShort, role, sessShort)
branch = fmt.Sprintf("deck/%s/%s-%s", objShort, role, idShort)

// After (per-stream):
name = fmt.Sprintf("deck-%s-%s", objShort, streamSlug)
branch = fmt.Sprintf("deck/%s/%s", objShort, streamSlug)
```

The `ReuseSandboxID` field on `SpawnRequest` is no longer needed for stream agents — the spawner handles reuse internally by querying sandbox labels.

### Daytona ExecuteCommand constraints

Daytona's `ExecuteCommand` does not support shell compound commands (`&&`, `||`, pipes, redirects). All commands must be individual entries:

```yaml
# Wrong (fails with exit 129):
post_create:
  - "git config user.name deck && git config user.email deck@localhost"

# Correct:
post_create:
  - "git config user.name deck"
  - "git config user.email deck@localhost"
```

The merger's git reset sequence must also use separate `Exec` calls, not `&&` chains.

### Resource budget per tier

| Tier | vCPUs | Sandbox vCPU | Max concurrent sandboxes | Max concurrent streams |
|------|-------|-------------|-------------------------|----------------------|
| 1 | 10 | 1 | 10 | 9 (1 for planner) |
| 2 | 100 | 1 | 100 | 99 |
| 3 | 250 | 1 | 250 | 249 |

The existing `agents.max_concurrent` config controls how many streams run in parallel. It should be set based on the tier (default: 4, which fits all tiers).

### Local sandbox impact

None. Local sandboxes (git worktrees) work identically — the spawner creates one worktree per stream instead of one per agent. Branch naming changes from role-based to stream-based. Merger reuses a worktree. All existing behavior preserved.

### What doesn't change

- Blueprint step flow (scout → build → lint → review → merge_ready)
- Quality gates (run in the stream's sandbox)
- Credential injection (same env vars)
- PTY/ExecStreaming for Pi (same approach)
- Auto-commit (same approach)
- PR creation (same approach, just uses the merger sandbox)

## Implementation

1. Add `streamSlug()` helper — sanitizes stream title to URL-safe name
2. Update spawner `Spawn` — look up sandbox by `deck.stream` label before creating
3. Update sandbox naming — `deck-{obj}-{streamSlug}` instead of `deck-{obj}-{role}-{id}`
4. Update branch naming — `deck/{obj}/{streamSlug}` instead of `deck/{obj}/{role}-{id}`
5. Remove `ReuseSandboxID` from coordinator's agent step handler (spawner handles it)
6. Update merger `getMergerSandbox` — split compound git commands into separate Exec calls
7. Update local provider `Create` — same naming changes
8. Test with Daytona Tier 1 (3-stream plan should use max 3 concurrent sandboxes)
