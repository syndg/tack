# Credentials, Config Layers & Daytona Sandbox Setup

**Date:** 2026-03-15
**Status:** Draft (addressing review feedback)
**Scope:** Unified credential management, two-layer config, Daytona sandbox lifecycle

---

## Problem

Tack has no credential management. Local sandboxes work by accident — they inherit the daemon's full `os.Environ()`, so `ANTHROPIC_API_KEY` leaks through. Daytona sandboxes only receive explicitly injected `TACK_*` vars, so agents fail immediately with no model API key.

Additionally:
- No `tack init` — users must hand-write `.tack/config.yaml`
- No separation between shared project config and personal settings
- No way to manage credentials for multiple model providers
- No onboarding experience
- Local sandbox `os.Environ()` inheritance leaks host secrets into agent processes

## Design

### Configuration Layers

Two layers. Project config is the source of truth; user config provides fallback defaults.

**Project** — `.tack/config.yaml` (git-tracked, shared with team):
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

**User** — `~/.config/tack/config.yaml` (personal, never tracked):
```yaml
daemon:
  listen: "127.0.0.1:9800"
  data_dir: "~/.config/tack/data"
agents:
  runtime: pi  # default when project doesn't specify
```

**Resolution:** Project wins → User fallback → Hardcoded defaults.

**Home directory:** `~/.config/tack/` is the canonical user home. This matches the existing CLI default paths (`root.go:34`, `main.go:17`), daemon blueprint/rule loading (`daemon.go:116`, `daemon.go:148`), and XDG conventions. No migration needed.

**Config discovery and merge algorithm:**

`config.Load` gains a new signature: `config.Load(projectPath, userPath string) (*Config, error)`.

1. Start with hardcoded defaults (`config.Default()`)
2. If `~/.config/tack/config.yaml` exists, deep-merge it over defaults (user layer)
3. If `.tack/config.yaml` exists, deep-merge it over the result (project layer wins)

Deep-merge rules:
- Scalar fields: later value replaces earlier
- Slices (quality_gates, post_create): later value replaces entirely (no append)
- Maps: merged key-by-key (e.g., `agents.timeouts.roles` merges per-role)

**Project root discovery:** Tack walks up from `cwd` looking for a `.tack/` directory (same pattern as `.git/` discovery). The first `.tack/config.yaml` found is the project config. If none found, Tack operates with user config + defaults only. The daemon also uses this rule — it resolves the project root at startup via `os.Getwd()`, which is consistent with the current behavior.

**`--config` flag:** The existing `--config` flag on `tack` and `daemon` is **redefined** as the project config path override. It replaces the walk-up discovery for that invocation. Equivalent to `TACK_CONFIG_PATH`. The user config path is only overridable via `TACK_USER_CONFIG_PATH` (rare escape hatch, not a flag).

```go
// Pseudocode
func Load(projectPath, userPath string) (*Config, error) {
    cfg := Default()                    // hardcoded defaults
    mergeFromFile(cfg, userPath)        // ~/.config/tack/config.yaml
    mergeFromFile(cfg, projectPath)     // .tack/config.yaml (wins)
    return cfg, nil
}
```

**`tack config` subcommand:**

Manages both layers via a single command. Default target is project config (most common action). `--user` flag targets the user layer. Project config location follows the same walk-up discovery as the rest of the CLI.

```
tack config set <key> <value>              # project .tack/config.yaml
tack config set --user <key> <value>       # user ~/.config/tack/config.yaml
tack config get <key>                      # resolved value (merged)
tack config get --user <key>               # user-layer value only
tack config get --project <key>            # project-layer value only
tack config list                           # all resolved config
tack config list --user                    # user config only
tack config remove <key>                   # remove from project config
tack config remove --user <key>            # remove from user config
```

Dotted keys for nested values:
```
tack config set agents.runtime pi
tack config set --user daemon.listen 127.0.0.1:9800
tack config get agents.pi.model
tack config remove quality_gates
```

### Credentials Store

Single file: `~/.config/tack/credentials.yaml` with `0600` permissions. Each entry is typed.

**Supported credential types:**

| Type | Use case | Fields |
|---|---|---|
| `api_key` | Standard API access (Anthropic Console, OpenAI, etc.) | `api_key` |
| `setup_token` | Anthropic subscription via `claude setup-token` | `token` |
| `pat` | Git host access (GitHub, GitLab, etc.) | `token`, `host` |
| `oauth` | (schema-only, deferred) Full OAuth with refresh | `access_token`, `refresh_token`, `expires_at` |

**Anthropic auth methods:**
- **API Key** (`type: api_key`) — from Anthropic Console (`sk-ant-api...`), usage-based billing
- **Setup Token** (`type: setup_token`) — from `claude setup-token` CLI command (`sk-ant-oat01-...`), subscription-based. Static token, no refresh needed. Works with both Pi and Claude Code runtimes. This is the same approach OpenClaw uses (`openclaw/src/commands/auth-choice.apply.anthropic.ts`).
- **Full OAuth** (`type: oauth`) — deferred. PKCE flow with auto-refresh, same flow Pi implements (`pi-mono/packages/ai/src/utils/oauth/anthropic.ts`, client ID `9d1c250a-...`, token URL `https://console.anthropic.com/v1/oauth/token`).

```yaml
model_providers:
  # Option 1: Anthropic API key (usage-based)
  anthropic:
    type: api_key
    api_key: "sk-ant-..."

  # Option 2: Anthropic subscription (via claude setup-token)
  # anthropic:
  #   type: setup_token
  #   token: "sk-ant-oat01-..."

  # Option 3: OAuth (deferred — schema reserved)
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
- `"!op read 'op://vault/tack/anthropic'"` — shell command (1Password, keychain, etc.)

Shell commands are executed once and cached for the process lifetime. Timeout: 10 seconds.

**Setup token validation:** Tokens with `sk-ant-oat01-` prefix must be ≥ 80 characters (same validation as OpenClaw `auth-token.ts`).

### Local Sandbox Environment Isolation

**Current problem:** `LocalSandbox.Exec()` and `ExecStreaming()` call `os.Environ()`, inheriting the daemon's full host environment. This leaks secrets that shouldn't reach agent processes.

**Fix:** Replace `os.Environ()` with a minimal allowlist. Local sandboxes get:

1. **System essentials** (allowlisted): `PATH`, `HOME`, `USER`, `SHELL`, `LANG`, `LC_*`, `TERM`, `TMPDIR`, `XDG_*`
2. **Sandbox-level env** (`s.envVars`): set at sandbox creation time
3. **Per-execution env** (`opts.Env`): set by spawner — `TACK_*` vars + resolved credentials

No other host environment variables pass through. This makes local sandboxes behave like Daytona sandboxes — agents only see what Tack explicitly provides.

**Post-create commands** (`provider.go:206`): Also use the restricted env (system essentials only + sandbox envVars). They don't need model credentials — they just install dependencies.

**Worktree management commands** (`git worktree add`, `git branch -D`, etc.): These run from the daemon process via `exec.Command`, not inside the sandbox, so they inherit the full daemon env. This is correct — they're Tack-internal operations, not agent-visible.

### Credential Injection

When the spawner creates an agent, it reads the provider from project config and maps it to the correct env var for the runtime.

**Mapping table** (hardcoded in Tack, initial scope — `api_key`, `setup_token`, and `pat`):

| Provider | Type | Env var injected |
|---|---|---|
| anthropic | api_key | `ANTHROPIC_API_KEY` |
| anthropic | setup_token | `ANTHROPIC_API_KEY` (setup tokens work via the same env var) |
| openai | api_key | `OPENAI_API_KEY` |
| gemini | api_key | `GEMINI_API_KEY` |
| groq | api_key | `GROQ_API_KEY` |
| mistral | api_key | `MISTRAL_API_KEY` |
| xai | api_key | `XAI_API_KEY` |

**Injection rules:**
- Model provider credential: only the one matching the configured provider
- Git credential: always injected as `GITHUB_TOKEN` (or `GITLAB_TOKEN` etc., derived from git host config)
- Sandbox credentials (Daytona): never injected — daemon-side only

**Initial scope:** `api_key`, `setup_token`, and `pat` types. The `oauth` type is reserved in the schema but not recognized at runtime — if present, Tack logs a warning: "OAuth credentials not yet supported, use api_key or setup_token".

**Flow:**
1. Spawner reads project config → `agents.pi.provider: anthropic`
2. Looks up `anthropic` in `~/.config/tack/credentials.yaml`
3. Resolves the value (literal / env var / shell command)
4. Maps to env var name for the runtime
5. Adds to sandbox env alongside `TACK_*` vars

### `tack init`

Interactive wizard using [charmbracelet/huh](https://github.com/charmbracelet/huh). Run once per project.

```
$ tack init

→ Which runtime? (pi / claude-code)
  > pi

→ Which model provider? (anthropic / openai / gemini / ...)
  > anthropic

→ Auth method? (api_key / setup_token)
  > setup_token

→ Run `claude setup-token` in another terminal, then paste the token:
  > sk-ant-oat01-...
  ✓ Validated (sk-ant-oat01- prefix, 120 chars)
  ✓ Stored in ~/.config/tack/credentials.yaml

→ GitHub token (for PRs, clone, push):
  > ghp_...
  ✓ Stored in ~/.config/tack/credentials.yaml

→ Sandbox provider? (local / daytona)
  > daytona

→ Daytona API key:
  > dtn_...
  ✓ Stored in ~/.config/tack/credentials.yaml

→ Post-create commands? (e.g., bun install, npm install)
  > bun install

✓ Created .tack/config.yaml
✓ Credentials saved to ~/.config/tack/credentials.yaml
```

Skips credential prompts for providers already in `~/.config/tack/credentials.yaml` (second project, same provider).

### `tack auth`

Standalone subcommand for ongoing credential management.

```
tack auth add <provider>       — add or replace a credential
tack auth remove <provider>    — remove a credential
tack auth list                 — show stored credentials (values masked)
tack auth test <provider>      — verify credential works (hit the API)
```

`tack auth refresh` deferred to the OAuth follow-up.

```
$ tack auth list
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
   - `sandbox.Git.CreateBranch()` + `sandbox.Git.Checkout()` for the tack working branch
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

**One-time:** `tack snapshot create`
1. Auto-detects repo URL from `git remote get-url origin`
2. Builds Daytona image: base OS + tooling + git clone (using stored git credential) + post-create commands (e.g., `bun install`)
3. Hashes the lockfile, stores hash in snapshot labels
4. Saves as a named Daytona snapshot

**Per-sandbox boot** (during execution, ~5s):
1. Create sandbox from snapshot (deps pre-installed, repo present)
2. Provider bootstrap: `sandbox.Git.Pull()` using git credential (delta only)
3. Provider bootstrap: `sandbox.Git.CreateBranch()` + `Checkout()` for `tack/{obj}/{role}-{id}`
4. Spawner injects model provider credential + git token as env vars
5. Runtime starts agent process

#### Staleness detection

Tack hashes the lockfile (`package-lock.json`, `bun.lockb`, `go.sum`, etc.) at snapshot creation time and stores it in the snapshot's labels.

At sandbox creation:
- Compare current lockfile hash with snapshot label
- If different: warn and fall back to running post-create commands after pull
- User can run `tack snapshot update` to rebuild

#### No-snapshot fallback

If no snapshot exists (first run or user skips snapshot creation):
1. Create sandbox from base image (ubuntu + git + runtime tooling)
2. Provider bootstrap: `sandbox.Git.Clone()` with git credential
3. Provider bootstrap: run post-create commands
4. Provider bootstrap: create + checkout tack branch
5. Spawner injects credentials, runtime starts agent

Slower (~30-60s) but works without any snapshot setup.

### File Layout

```
# Project (git-tracked)
.tack/
  config.yaml          # runtime, provider, gates, blueprints, post-create
  blueprints/          # custom blueprints (optional)
  rules/               # file-scope rules (optional)

# User (personal, never tracked)
~/.config/tack/
  config.yaml          # fallback defaults (listen addr, data dir)
  credentials.yaml     # all credentials (0600 perms)
  blueprints/          # user-level blueprint overrides
  rules/               # user-level rule overrides
  data/                # SQLite DB, activity logs
```

### Tests Required

1. **Local env isolation** — regression test: local sandbox `Exec` does NOT inherit `SUPER_SECRET_HOST_VAR` from `os.Environ()`, only receives allowlisted system vars + explicitly injected vars
2. **Config precedence** — project config overrides user config; user config overrides defaults; credentials resolve literal/env/shell correctly
3. **Project root discovery** — walk-up from nested subdirectory finds `.tack/config.yaml`; `--config` flag overrides discovery
4. **Credential injection** — spawner injects correct env var per provider/type (`api_key` and `setup_token`); `setup_token` validated and injected as `ANTHROPIC_API_KEY`; git token always injected; daytona key never injected
5. **Daytona bootstrap with snapshot** — provider creates from snapshot, pulls latest, checks out branch, post-create runs
6. **Daytona bootstrap without snapshot** — provider creates from image, clones repo, installs deps, checks out branch

### Implementation Order

1. **Credentials store** — `~/.config/tack/credentials.yaml` read/write, value resolution (literal/env/shell), `0600` permissions
2. **Config layering** — project root walk-up discovery, project + user config merge with project-wins precedence
3. **`tack config`** — set, get, list, remove with `--user` flag
4. **Local env isolation** — replace `os.Environ()` with minimal allowlist in `LocalSandbox.Exec/ExecStreaming`
5. **Credential injection** — spawner reads provider from config, maps to env var, injects into sandbox
6. **`tack auth`** — add, remove, list, test subcommands (huh for interactive prompts)
7. **`tack init`** — interactive wizard with charmbracelet/huh
8. **Daytona bootstrap** — provider owns clone/fetch + branch checkout + post-create
9. **`tack snapshot`** — create, update, staleness detection

### Deferred (follow-up)

- **Full OAuth (PKCE + auto-refresh):** Adds `tack auth refresh`, `tack auth login <provider>` (browser-based), and spawn-time auto-refresh. When implemented, adds `anthropic | oauth | ANTHROPIC_OAUTH_TOKEN` to the mapping table. Pi reads `ANTHROPIC_OAUTH_TOKEN` as a separate env var (verified in `pi-mono/packages/ai/src/env-api-keys.ts:71-73`).

  **Reference implementation from Pi** (`pi-mono/packages/ai/src/utils/oauth/anthropic.ts`):
  - Client ID: `9d1c250a-e61b-44d9-88ed-5944d1962f5e`
  - Auth URL: `https://claude.ai/oauth/authorize`
  - Token URL: `https://console.anthropic.com/v1/oauth/token`
  - Scopes: `org:create_api_key user:profile user:inference`
  - PKCE with S256 challenge method
  - Refresh via `POST /v1/oauth/token` with `grant_type=refresh_token`
  - File-locked token storage (`proper-lockfile`) for multi-instance safety

  **Reference implementation from OpenClaw** (`openclaw/src/agents/auth-profiles/oauth.ts`):
  - Reuses Pi's OAuth refresh flow via `@mariozechner/pi-ai/oauth`
  - File-locked atomic updates under `withFileLock()`

- **Claude Code OAuth:** Verify `ANTHROPIC_OAUTH_TOKEN` env var support in Claude Code before enabling OAuth for the `claude-code` runtime.

- **Additional providers from OpenCode** (`opencode/internal/llm/provider/`):
  - Azure OpenAI: API key + Entra ID (Azure AD) credential support
  - AWS Bedrock: full AWS credential chain (IAM, profiles, instance roles)
  - Google VertexAI: GCP Application Default Credentials
  - GitHub Copilot: GitHub token → Copilot bearer token exchange (`api.github.com/copilot_internal/v2/token`)

- **Multi-host git:** Schema supports `hosts` map. Initial implementation targets single-host (`host` + `token` fields). Multi-host added when a user needs it.
