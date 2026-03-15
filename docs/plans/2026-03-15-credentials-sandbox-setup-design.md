# Credentials, Config Layers & Daytona Sandbox Setup

**Date:** 2026-03-15
**Status:** Draft (addressing review feedback)
**Scope:** Unified credential management, two-layer config, Daytona sandbox lifecycle

---

## Problem

Deck has no credential management. Local sandboxes work by accident — they inherit the daemon's full `os.Environ()`, so `ANTHROPIC_API_KEY` leaks through. Daytona sandboxes only receive explicitly injected `DECK_*` vars, so agents fail immediately with no model API key.

Additionally:
- No `deck init` — users must hand-write `.deck/config.yaml`
- No separation between shared project config and personal settings
- No way to manage credentials for multiple model providers
- No onboarding experience
- Local sandbox `os.Environ()` inheritance leaks host secrets into agent processes

## Design

### Configuration Layers

Two layers. Project config is the source of truth; user config provides fallback defaults.

**Project** — `.deck/config.yaml` (git-tracked, shared with team):
```yaml
sandbox:
  provider: daytona
  post_create:
    - bun install
agents:
  runtime: pi
  pi:
    provider: anthropic
    model: claude-sonnet-4-20250514
planning:
  model: claude-sonnet-4-20250514
quality_gates:
  - bun run lint
  - bun run test
```

**User** — `~/.config/deck/config.yaml` (personal, never tracked):
```yaml
daemon:
  listen: "127.0.0.1:9800"
  data_dir: "~/.config/deck/data"
agents:
  runtime: pi  # default when project doesn't specify
```

**Resolution:** Project wins → User fallback → Hardcoded defaults.

**Home directory:** `~/.config/deck/` is the canonical user home. This matches the existing CLI default paths (`root.go:34`, `main.go:17`), daemon blueprint/rule loading (`daemon.go:116`, `daemon.go:148`), and XDG conventions. No migration needed.

**Config discovery and merge algorithm:**

`config.Load` gains a new signature: `config.Load(projectPath, userPath string) (*Config, error)`.

1. Start with hardcoded defaults (`config.Default()`)
2. If `~/.config/deck/config.yaml` exists, deep-merge it over defaults (user layer)
3. If `.deck/config.yaml` exists, deep-merge it over the result (project layer wins)

Deep-merge rules:
- Scalar fields: later value replaces earlier
- Slices (quality_gates, post_create): later value replaces entirely (no append)
- Maps: merged key-by-key (e.g., `agents.timeouts.roles` merges per-role)

**Project root discovery:** Deck walks up from `cwd` looking for a `.deck/` directory (same pattern as `.git/` discovery). The first `.deck/config.yaml` found is the project config. If none found, Deck operates with user config + defaults only. The daemon also uses this rule — it resolves the project root at startup via `os.Getwd()`, which is consistent with the current behavior.

**`--config` flag:** The existing `--config` flag on `deck` and `deck-daemon` is **redefined** as the project config path override. It replaces the walk-up discovery for that invocation. Equivalent to `DECK_CONFIG_PATH`. The user config path is only overridable via `DECK_USER_CONFIG_PATH` (rare escape hatch, not a flag).

```go
// Pseudocode
func Load(projectPath, userPath string) (*Config, error) {
    cfg := Default()                    // hardcoded defaults
    mergeFromFile(cfg, userPath)        // ~/.config/deck/config.yaml
    mergeFromFile(cfg, projectPath)     // .deck/config.yaml (wins)
    return cfg, nil
}
```

**`deck config` subcommand:**

Manages both layers via a single command. Default target is project config (most common action). `--user` flag targets the user layer. Project config location follows the same walk-up discovery as the rest of the CLI.

```
deck config set <key> <value>              # project .deck/config.yaml
deck config set --user <key> <value>       # user ~/.config/deck/config.yaml
deck config get <key>                      # resolved value (merged)
deck config get --user <key>               # user-layer value only
deck config get --project <key>            # project-layer value only
deck config list                           # all resolved config
deck config list --user                    # user config only
deck config remove <key>                   # remove from project config
deck config remove --user <key>            # remove from user config
```

Dotted keys for nested values:
```
deck config set agents.runtime pi
deck config set --user daemon.listen 127.0.0.1:9800
deck config get agents.pi.model
deck config remove quality_gates
```

### Credentials Store

Single file: `~/.config/deck/credentials.yaml` with `0600` permissions. Each entry is typed.

```yaml
model_providers:
  anthropic:
    type: api_key
    api_key: "sk-ant-..."

  # OR with OAuth (Anthropic subscription):
  # anthropic:
  #   type: oauth
  #   access_token: "..."
  #   refresh_token: "..."
  #   expires_at: 1734567890

  openai:
    type: api_key
    api_key: "sk-..."

git:
  type: pat
  host: "github.com"       # scoped to host, not hardcoded to GitHub
  token: "ghp_..."

  # Multi-host example:
  # git:
  #   type: pat
  #   hosts:
  #     github.com:
  #       token: "ghp_..."
  #     gitlab.mycompany.com:
  #       token: "glpat-..."

sandbox:
  daytona:
    type: api_key
    api_key: "dtn_..."
```

**Sections:**
- `model_providers` — keyed by provider name, injected into sandboxes per runtime
- `git` — host-scoped PAT for clone, fetch, push, PR creation. Defaults to `github.com` for single-host config. Supports `hosts` map for multi-host setups.
- `sandbox` — daemon-side only, never injected into sandboxes

**Value resolution** — any string value supports three formats:
- `"sk-ant-..."` — literal value
- `"ANTHROPIC_API_KEY"` — environment variable lookup
- `"!op read 'op://vault/deck/anthropic'"` — shell command (1Password, keychain, etc.)

Shell commands are executed once and cached for the process lifetime. Timeout: 10 seconds.

### Local Sandbox Environment Isolation

**Current problem:** `LocalSandbox.Exec()` and `ExecStreaming()` call `os.Environ()`, inheriting the daemon's full host environment. This leaks secrets that shouldn't reach agent processes.

**Fix:** Replace `os.Environ()` with a minimal allowlist. Local sandboxes get:

1. **System essentials** (allowlisted): `PATH`, `HOME`, `USER`, `SHELL`, `LANG`, `LC_*`, `TERM`, `TMPDIR`, `XDG_*`
2. **Sandbox-level env** (`s.envVars`): set at sandbox creation time
3. **Per-execution env** (`opts.Env`): set by spawner — `DECK_*` vars + resolved credentials

No other host environment variables pass through. This makes local sandboxes behave like Daytona sandboxes — agents only see what Deck explicitly provides.

**Post-create commands** (`provider.go:206`): Also use the restricted env (system essentials only + sandbox envVars). They don't need model credentials — they just install dependencies.

**Worktree management commands** (`git worktree add`, `git branch -D`, etc.): These run from the daemon process via `exec.Command`, not inside the sandbox, so they inherit the full daemon env. This is correct — they're Deck-internal operations, not agent-visible.

### Credential Injection

When the spawner creates an agent, it reads the provider from project config and maps it to the correct env var for the runtime.

**Mapping table** (hardcoded in Deck):

| Provider | Type | Env var injected |
|---|---|---|
| anthropic | api_key | `ANTHROPIC_API_KEY` |
| anthropic | oauth | `ANTHROPIC_OAUTH_TOKEN` |
| openai | api_key | `OPENAI_API_KEY` |
| gemini | api_key | `GEMINI_API_KEY` |
| groq | api_key | `GROQ_API_KEY` |
| mistral | api_key | `MISTRAL_API_KEY` |
| xai | api_key | `XAI_API_KEY` |

**OAuth env var:** Pi reads `ANTHROPIC_OAUTH_TOKEN` as a separate env var (checked before `ANTHROPIC_API_KEY` in `pi-mono/packages/ai/src/env-api-keys.ts:71-73`). Deck injects the access token as `ANTHROPIC_OAUTH_TOKEN`, not `ANTHROPIC_API_KEY`. This is verified for Pi. Claude Code's support for `ANTHROPIC_OAUTH_TOKEN` is **assumed but unverified** — must be validated before OAuth ships for the claude-code runtime.

**Injection rules:**
- Model provider credential: only the one matching the configured provider
- Git credential: always injected as `GITHUB_TOKEN` (or `GITLAB_TOKEN` etc., derived from git host config)
- Sandbox credentials (Daytona): never injected — daemon-side only

**OAuth is fully out of initial scope.** The credential store schema supports the `oauth` type (so adding it later doesn't require a schema migration), but the initial implementation only recognizes `api_key` and `pat` types. `deck init` and `deck auth add` do not offer OAuth as an auth method. If a credentials file contains an `oauth` entry (e.g., hand-edited), Deck ignores it and logs a warning: "OAuth credentials not yet supported, use api_key".

**Flow:**
1. Spawner reads project config → `agents.pi.provider: anthropic`
2. Looks up `anthropic` in `~/.config/deck/credentials.yaml`
3. Resolves the value (literal / env var / shell command)
4. Maps to env var name for the runtime
5. Adds to sandbox env alongside `DECK_*` vars

### `deck init`

Interactive wizard using [charmbracelet/huh](https://github.com/charmbracelet/huh). Run once per project.

```
$ deck init

→ Which runtime? (pi / claude-code)
  > pi

→ Which model provider? (anthropic / openai / gemini / ...)
  > anthropic

→ Anthropic API key:
  > sk-ant-...
  ✓ Stored in ~/.config/deck/credentials.yaml

→ GitHub token (for PRs, clone, push):
  > ghp_...
  ✓ Stored in ~/.config/deck/credentials.yaml

→ Sandbox provider? (local / daytona)
  > daytona

→ Daytona API key:
  > dtn_...
  ✓ Stored in ~/.config/deck/credentials.yaml

→ Post-create commands? (e.g., bun install, npm install)
  > bun install

✓ Created .deck/config.yaml
✓ Credentials saved to ~/.config/deck/credentials.yaml
```

Skips credential prompts for providers already in `~/.config/deck/credentials.yaml` (second project, same provider).

### `deck auth`

Standalone subcommand for ongoing credential management.

```
deck auth add <provider>       — add or replace a credential
deck auth remove <provider>    — remove a credential
deck auth list                 — show stored credentials (values masked)
deck auth test <provider>      — verify credential works (hit the API)
```

`deck auth refresh` deferred to the OAuth follow-up.

```
$ deck auth list
  anthropic    api_key   ✓ valid
  openai       api_key   ✓ valid
  github       pat       ✓ valid
  daytona      api_key   ✓ valid
```

### Daytona Sandbox Setup

#### Bootstrap ownership

The **Daytona sandbox provider** owns repo bootstrap. Today the provider only calls `client.Create()`. After this design, `Create()` gains a post-creation bootstrap phase:

1. `client.Create()` — provision the sandbox (from snapshot or image)
2. **Bootstrap phase** (new, owned by provider):
   - If snapshot: `sandbox.Git.Pull()` to fetch latest (credentials passed via SDK options)
   - If no snapshot: `sandbox.Git.Clone()` the repo (credentials passed via SDK options)
   - `sandbox.Git.CreateBranch()` + `sandbox.Git.Checkout()` for the deck working branch
   - Run post-create commands via `sandbox.Process.ExecuteCommand()`
3. Return ready sandbox to spawner

The spawner doesn't change — it calls `sandboxProv.Create()` and gets back a sandbox with code + deps ready. The runtime then uploads extensions and starts the agent process as before.

**Credential flow for bootstrap:** The provider reads git credentials from the credentials store (passed in at construction time). These are used for SDK Git operations (`WithUsername`/`WithPassword` options) during bootstrap. They are NOT injected as env vars at this stage — that happens later when the spawner sets up the agent process.

**New provider constructor:**
```go
func New(cfg Config, creds *credentials.Store, logger *slog.Logger) (*Provider, error)
```

The `credentials.Store` is a read-only interface that resolves credentials by name.

#### Snapshot model (speed + freshness)

**One-time:** `deck snapshot create`
1. Auto-detects repo URL from `git remote get-url origin`
2. Builds Daytona image: base OS + tooling + git clone (using stored git credential) + post-create commands (e.g., `bun install`)
3. Hashes the lockfile, stores hash in snapshot labels
4. Saves as a named Daytona snapshot

**Per-sandbox boot** (during execution, ~5s):
1. Create sandbox from snapshot (deps pre-installed, repo present)
2. Provider bootstrap: `sandbox.Git.Pull()` using git credential (delta only)
3. Provider bootstrap: `sandbox.Git.CreateBranch()` + `Checkout()` for `deck/{obj}/{role}-{id}`
4. Spawner injects model provider credential + git token as env vars
5. Runtime starts agent process

#### Staleness detection

Deck hashes the lockfile (`package-lock.json`, `bun.lockb`, `go.sum`, etc.) at snapshot creation time and stores it in the snapshot's labels.

At sandbox creation:
- Compare current lockfile hash with snapshot label
- If different: warn and fall back to running post-create commands after pull
- User can run `deck snapshot update` to rebuild

#### No-snapshot fallback

If no snapshot exists (first run or user skips snapshot creation):
1. Create sandbox from base image (ubuntu + git + runtime tooling)
2. Provider bootstrap: `sandbox.Git.Clone()` with git credential
3. Provider bootstrap: run post-create commands
4. Provider bootstrap: create + checkout deck branch
5. Spawner injects credentials, runtime starts agent

Slower (~30-60s) but works without any snapshot setup.

### File Layout

```
# Project (git-tracked)
.deck/
  config.yaml          # runtime, provider, gates, blueprints, post-create
  blueprints/          # custom blueprints (optional)
  rules/               # file-scope rules (optional)

# User (personal, never tracked)
~/.config/deck/
  config.yaml          # fallback defaults (listen addr, data dir)
  credentials.yaml     # all credentials (0600 perms)
  blueprints/          # user-level blueprint overrides
  rules/               # user-level rule overrides
  data/                # SQLite DB, activity logs
```

### Tests Required

1. **Local env isolation** — regression test: local sandbox `Exec` does NOT inherit `SUPER_SECRET_HOST_VAR` from `os.Environ()`, only receives allowlisted system vars + explicitly injected vars
2. **Config precedence** — project config overrides user config; user config overrides defaults; credentials resolve literal/env/shell correctly
3. **Project root discovery** — walk-up from nested subdirectory finds `.deck/config.yaml`; `--config` flag overrides discovery
4. **Credential injection** — spawner injects correct env var per provider/type (`api_key` only for initial scope); git token always injected; daytona key never injected
5. **Daytona bootstrap with snapshot** — provider creates from snapshot, pulls latest, checks out branch, post-create runs
6. **Daytona bootstrap without snapshot** — provider creates from image, clones repo, installs deps, checks out branch

### Implementation Order

1. **Credentials store** — `~/.config/deck/credentials.yaml` read/write, value resolution (literal/env/shell), `0600` permissions
2. **Config layering** — project root walk-up discovery, project + user config merge with project-wins precedence
3. **`deck config`** — set, get, list, remove with `--user` flag
4. **Local env isolation** — replace `os.Environ()` with minimal allowlist in `LocalSandbox.Exec/ExecStreaming`
5. **Credential injection** — spawner reads provider from config, maps to env var, injects into sandbox
6. **`deck auth`** — add, remove, list, test subcommands (huh for interactive prompts)
7. **`deck init`** — interactive wizard with charmbracelet/huh
8. **Daytona bootstrap** — provider owns clone/fetch + branch checkout + post-create
9. **`deck snapshot`** — create, update, staleness detection

### Deferred (follow-up)

- **OAuth:** Auto-refresh, `deck auth refresh`, PKCE token exchange. Schema supports `oauth` type from day one. Refresh flow requires implementing Anthropic's token exchange (same as Pi's `anthropic.ts`). Ships after API key path is solid end-to-end.
- **Claude Code OAuth:** Verify `ANTHROPIC_OAUTH_TOKEN` env var support in Claude Code before enabling OAuth for the `claude-code` runtime.
- **Multi-host git:** Schema supports `hosts` map. Initial implementation targets single-host (`host` + `token` fields). Multi-host added when a user needs it.
