# Security Audit - 2026-04-10

## Scope

- Daemon HTTP surface
- Agent/auth plumbing
- Sandbox credential handling
- Observability/event leakage

## Final Status

- Fixed: daemon default bind address now `127.0.0.1:9800`
- Fixed: daemon routes now require bearer auth via shared daemon token
- Fixed: CLI and agents now automatically send daemon auth token
- Fixed: mail send/read paths now enforce project scoping
- Fixed: Daytona no longer stores git PATs in `origin` remote URLs
- Fixed: projected live agent activity events no longer include raw `content`
- Fixed: observability log files now use `0600`
- Fixed: local sandbox `WorkDir` / `Upload` / `Download` path traversal outside the worktree
- Fixed: Daytona sandbox `WorkDir` / `Upload` / `Download` path traversal outside the repo root
- Partially mitigated: canonical on-disk observability records still retain raw activity content for local replay/debugging
- Residual risk: if an operator explicitly binds the daemon to a non-local interface and exposes the token, the control plane remains highly privileged by design

## Findings

### 1. Critical: daemon is remotely reachable by default and has no authentication

Status: Fixed

Files:
- `internal/config/config.go:356`
- `internal/daemon/daemon.go`
- `internal/daemon/auth.go`

What happens:
- Previously, default config bound the daemon to `0.0.0.0:9800` and installed an unauthenticated control plane.
- Now the default bind is loopback-only and the daemon wraps routes in bearer-token auth middleware.

Impact:
- Original remote unauthenticated control-plane exposure is closed by default.
- Remaining risk is configuration-driven: operators can still choose a non-local listen address.

Exploit sketch:
- Old exploit path no longer works without the daemon token.

Notes:
- Token is auto-created at `~/.config/tack/daemon-token` unless `TACK_DAEMON_TOKEN` is set.

### 2. High: agent bearer token is generated but never enforced

Status: Fixed

Files:
- `internal/services/dispatch/spawner.go`
- `internal/runtime/pi/extension/index.ts`
- `internal/daemon/auth.go`

What happens:
- Previously, agent callbacks sent bearer auth but the daemon ignored it.
- Now daemon auth is enforced consistently and agents receive the daemon token automatically.

Impact:
- Unauthenticated agent impersonation is no longer possible without the daemon token.

Exploit sketch:
- Old exploit path now returns `401` without a valid token.

### 3. High: mail endpoints allow cross-project spoofing and tampering

Status: Fixed

Files:
- `internal/daemon/routes_mail.go`
- `internal/db/mail.go`

What happens:
- `handleSendMail()` now requires the targeted project context and rejects mismatched `project_id` values.
- Objective IDs are validated against the targeted project before mail is accepted.
- `handleMarkMailRead()` now loads the message first and enforces project match before marking it read.
- Mail routes also sit behind daemon auth now.

Impact:
- Cross-project spoofing and unauthenticated tampering paths are closed.

Exploit sketch:
- Old exploit path now fails on auth and project-match checks.

### 4. High: Daytona mode stores the git PAT in the sandbox remote URL

Status: Fixed

Files:
- `internal/sandbox/daytona/provider.go`

What happens:
- Tack no longer rewrites `origin` to embed credentials.
- Git auth is now injected per git command via `GIT_CONFIG_*` and `http.extraHeader`.

Impact:
- The straightforward `git remote get-url origin` credential leak is removed.
- Residual risk remains for any process that can inspect its own environment or command context during a live git invocation, but the persistent-at-rest secret leak in repo config is gone.

Exploit sketch:
- Old exploit path no longer yields credentials from git remote configuration.

Notes:
- This is especially dangerous because Tack is explicitly designed to run model-directed commands in those sandboxes.

### 5. Medium: unauthenticated SSE and activity logs leak sensitive operational data

Status: Mostly fixed, partially mitigated

Files:
- `internal/daemon/sse.go`
- `internal/observability/recorder.go`
- `internal/services/mail/broker.go`
- `internal/services/dispatch/agent_tracker.go`

What happens:
- `/events` now requires daemon auth because all daemon routes are wrapped by auth middleware.
- Project activity logs now use mode `0600`.
- Escalation milestones no longer project raw mail bodies/payloads.
- Live projected `agent.activity` events no longer include raw `content`.
- Canonical on-disk activity records still retain `content` for replay/debugging.

Impact:
- Passive network observation is substantially reduced.
- Local disk exposure is reduced via file permissions, but highly sensitive tool args can still exist in canonical local logs.

Exploit sketch:
- Old unauthenticated SSE path is closed.
- Remaining local-risk path would require same-user or privileged access to the filesystem.

### 6. High: local sandbox path traversal could escape the worktree

Status: Fixed

Files:
- `internal/sandbox/local/provider.go`
- `internal/sandbox/local/provider_test.go`

What happens:
- Previously, local sandbox `Exec`, `ExecStreaming`, `Upload`, and `Download` used `filepath.Join` without a containment check.
- Inputs such as `../..` could escape the sandbox worktree and reach arbitrary host paths accessible to the running user.

Impact:
- A malicious or compromised agent could read/write files outside the intended worktree on the host.
- This was a real host-side breakout from the sandbox abstraction in local mode.

Exploit sketch:
1. Call `Upload("../../.ssh/authorized_keys")` or `Download("../../.env")`.
2. Or set `ExecOpts.WorkDir` to `../..` and run commands from outside the worktree.

Remediation:
- Added containment checks with `resolveWithin()` to reject any path that escapes the sandbox root.

### 7. Medium: Daytona sandbox path traversal could escape the repo root

Status: Fixed

Files:
- `internal/sandbox/daytona/provider.go`
- `internal/sandbox/daytona/provider_test.go`

What happens:
- Previously, Daytona `Exec`, `ExecStreaming`, `Upload`, and `Download` accepted relative and absolute paths without checking containment under the configured repo root.
- Relative paths like `../../etc/passwd` or absolute paths like `/etc/passwd` could target files outside the intended repo path inside the remote sandbox.

Impact:
- A malicious or compromised agent could interact with files elsewhere in the remote sandbox filesystem instead of being constrained to the repo root.

Remediation:
- Added `resolveRemotePath()` to constrain requested paths to the configured Daytona repo root for working directories and file transfer operations.

## Most Likely Real Attack Chain

Original chain is largely closed by loopback default, daemon bearer auth, mail scoping checks, and PAT removal from Daytona remotes.

Remaining realistic chain:
1. Obtain the daemon token or local filesystem access as the same user.
2. Use the authenticated control plane intentionally exposed by Tack to operate projects.
3. Read canonical local observability logs if same-user access is already available.

## Short Remediation Order

Completed:
1. Bind to loopback by default.
2. Add bearer auth for daemon routes.
3. Enforce daemon auth for agents/CLI.
4. Remove PATs from Daytona remote URLs.
5. Lock down `/events` and reduce projected observability leakage.
6. Enforce project scoping on mail operations.
7. Add local sandbox path containment checks.

Remaining hardening ideas:
1. Optionally encrypt or redact canonical on-disk activity `content`.
2. Consider narrower route-level authz instead of one shared daemon token for all privileged operations.
