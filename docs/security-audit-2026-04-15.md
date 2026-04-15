# Security Audit - 2026-04-15

## Scope

- Shell command construction in runtime, merge, and PR flows
- Local sandbox ignored-file bootstrap behavior
- Documentation accuracy for local sandbox trust boundaries

## Final Status

- Fixed: shell interpolation now quotes repo/config-derived branch names, refs, model names, and PR base/head values before they reach `sh -c`
- Fixed: local ignored-file bootstrap no longer recreates ignored symlinks that resolve outside the repository root
- Fixed: security docs no longer claim local worktrees prevent access outside the worktree or provide hard file-scope enforcement
- Residual risk: local sandboxes still run as the invoking user and are not kernel/container isolation
- Residual risk: project-owned shell surfaces such as `project_setup.commands`, `quality_gates`, and similar repo configuration remain trusted code execution inputs by design

## Findings

### 1. High: repo-controlled branch/ref/model values reached `sh -c` without quoting

Status: Fixed

Files:
- `internal/services/dispatch/execution_runner.go`
- `internal/services/dispatch/execution_lifecycle.go`
- `internal/services/dispatch/handlers.go`
- `internal/services/merge/processor.go`
- `internal/services/merge/git.go`
- `internal/services/merge/diff.go`
- `internal/runtime/pi/runtime.go`
- `internal/runtime/claudecode/runtime.go`
- `internal/sandbox/daytona/provider.go`

What happened:
- Tack builds many sandbox commands through `sh -c`.
- Several of those commands embedded branch names, merge refs, diff refs, model names, and base-branch config values directly into shell strings.
- A malicious repo or config could supply values containing shell metacharacters and turn normal merge/runtime operations into unintended command execution.

Impact:
- In local mode, this could become arbitrary command execution as the current user.
- In remote mode, it could become arbitrary command execution inside the remote sandbox.

Remediation:
- All repo/config-derived values that reach shell command strings are now passed through `naming.ShellQuote()` before execution.

### 2. High: ignored symlink bootstrap could expose host files inside local worktrees

Status: Fixed

Files:
- `internal/sandbox/local/copyignored.go`

What happened:
- Local worktree bootstrap copies ignored files from the main repo into fresh worktrees for warm caches.
- Symlinks were recreated verbatim, even when the symlink resolved outside the repository root.
- A repo could place an ignored symlink to a sensitive host file and have that symlink appear inside the agent worktree.

Impact:
- Agents in local mode could read or follow the copied link and reach files outside the repo boundary.

Remediation:
- Ignored symlinks are now resolved before copy.
- If the resolved target escapes the repository root, the symlink is skipped.

### 3. Medium: prior security docs overstated local sandbox guarantees

Status: Fixed in docs

Files:
- `docs-site/content/docs/security.mdx`

What happened:
- The previous page implied local worktrees prevent access outside the worktree and enforce file scope as a hard boundary.
- In reality, local mode is an orchestrated worktree plus tool/runtime policy, not OS-level isolation.

Impact:
- Operators could over-trust local mode when running Tack in repos they do not fully trust.

Remediation:
- Docs now explicitly state that local sandboxes run as your user.
- File scope is described as an orchestration/tooling boundary, not a kernel-enforced sandbox.
- The page now calls out repo-owned shell surfaces as trusted inputs.
