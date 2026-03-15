# Credentials, Config Layers & Daytona Sandbox Setup

**Date:** 2026-03-15
**Status:** Approved
**Scope:** Unified credential management, two-layer config, Daytona sandbox lifecycle

---

## Problem

Deck has no credential management. Local sandboxes work by accident — they inherit the daemon's full `os.Environ()`, so `ANTHROPIC_API_KEY` leaks through. Daytona sandboxes only receive explicitly injected `DECK_*` vars, so agents fail immediately with no model API key.

Additionally:
- No `deck init` — users must hand-write `.deck/config.yaml`
- No separation between shared project config and personal settings
- No way to manage credentials for multiple model providers
- No onboarding experience

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

**User** — `~/.deck/config.yaml` (personal, never tracked):
```yaml
daemon:
  listen: "127.0.0.1:9800"
  data_dir: "~/.deck/data"
agents:
  runtime: pi  # default when project doesn't specify
```

**Resolution:** Project wins → User fallback → Hardcoded defaults.

### Credentials Store

Single file: `~/.deck/credentials.yaml` with `0600` permissions. Each entry is typed.

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
  token: "ghp_..."

sandbox:
  daytona:
    type: api_key
    api_key: "dtn_..."
```

**Sections:**
- `model_providers` — keyed by provider name, injected into sandboxes per runtime
- `git` — GitHub PAT for clone, fetch, push, PR creation, issue management
- `sandbox` — daemon-side only, never injected into sandboxes

**Value resolution** — any string value supports three formats:
- `"sk-ant-..."` — literal value
- `"ANTHROPIC_API_KEY"` — environment variable lookup
- `"!op read 'op://vault/deck/anthropic'"` — shell command (1Password, keychain, etc.)

Shell commands are executed once and cached for the process lifetime. Timeout: 10 seconds.

### Credential Injection

When the spawner creates an agent, it reads the provider from project config and maps it to the correct env var for the runtime.

**Mapping table** (hardcoded in Deck):

| Provider | Type | Env var injected |
|---|---|---|
| anthropic | api_key | `ANTHROPIC_API_KEY` |
| anthropic | oauth | `ANTHROPIC_API_KEY` (access_token) |
| openai | api_key | `OPENAI_API_KEY` |
| gemini | api_key | `GEMINI_API_KEY` |
| groq | api_key | `GROQ_API_KEY` |
| mistral | api_key | `MISTRAL_API_KEY` |
| xai | api_key | `XAI_API_KEY` |

**Injection rules:**
- Model provider credential: only the one matching the configured provider
- Git credential (`GITHUB_TOKEN`): always injected — agents need GitHub access
- Sandbox credentials (Daytona): never injected — daemon-side only

**OAuth auto-refresh:** If the stored access token is expired, Deck refreshes it using the refresh token before injection. If refresh fails, agent spawn fails with: "Anthropic OAuth token expired, run `deck auth refresh anthropic`".

**Flow:**
1. Spawner reads project config → `agents.pi.provider: anthropic`
2. Looks up `anthropic` in `~/.deck/credentials.yaml`
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

→ Auth method for anthropic? (api_key / oauth)
  > api_key

→ Anthropic API key:
  > sk-ant-...
  ✓ Stored in ~/.deck/credentials.yaml

→ GitHub token (for PRs, clone, push):
  > ghp_...
  ✓ Stored in ~/.deck/credentials.yaml

→ Sandbox provider? (local / daytona)
  > daytona

→ Daytona API key:
  > dtn_...
  ✓ Stored in ~/.deck/credentials.yaml

→ Post-create commands? (e.g., bun install, npm install)
  > bun install

✓ Created .deck/config.yaml
✓ Credentials saved to ~/.deck/credentials.yaml
```

Skips credential prompts for providers already in `~/.deck/credentials.yaml` (second project, same provider).

### `deck auth`

Standalone subcommand for ongoing credential management.

```
deck auth add <provider>       — add or replace a credential
deck auth remove <provider>    — remove a credential
deck auth list                 — show stored credentials (values masked)
deck auth refresh <provider>   — refresh an OAuth token
deck auth test <provider>      — verify credential works (hit the API)
```

```
$ deck auth list
  anthropic    oauth     ✓ valid (expires in 12d)
  openai       api_key   ✓ valid
  github       pat       ✓ valid
  daytona      api_key   ✓ valid
```

### Daytona Sandbox Setup

#### Snapshot model (speed + freshness)

**One-time:** `deck snapshot create`
1. Auto-detects repo URL from `git remote get-url origin`
2. Builds Daytona image: base OS + tooling + git clone (using stored GitHub token) + post-create commands (e.g., `bun install`)
3. Hashes the lockfile, stores hash in snapshot labels
4. Saves as a named Daytona snapshot

**Per-sandbox boot** (during execution, ~5s):
1. Create sandbox from snapshot (deps pre-installed, repo present)
2. `git fetch origin` using stored GitHub token (delta only)
3. `git checkout -b deck/{obj}/{role}-{id} origin/{base_branch}`
4. Inject model provider credential + `GITHUB_TOKEN` as env vars
5. Agent starts

#### Staleness detection

Deck hashes the lockfile (`package-lock.json`, `bun.lockb`, `go.sum`, etc.) at snapshot creation time and stores it in the snapshot's labels.

At sandbox creation:
- Compare current lockfile hash with snapshot label
- If different: warn and fall back to running post-create commands after fetch
- User can run `deck snapshot update` to rebuild

#### No-snapshot fallback

If no snapshot exists (first run or user skips snapshot creation):
1. Create sandbox from base image (ubuntu + git + runtime tooling)
2. `git clone` the repo using GitHub token
3. Run post-create commands (`bun install`)
4. Checkout branch
5. Agent starts

Slower (~30-60s) but works without any snapshot setup.

### File Layout

```
# Project (git-tracked)
.deck/
  config.yaml          # runtime, provider, gates, blueprints, post-create
  blueprints/          # custom blueprints (optional)
  rules/               # file-scope rules (optional)

# User (personal, never tracked)
~/.deck/
  config.yaml          # fallback defaults (listen addr, data dir)
  credentials.yaml     # all credentials (0600 perms)
  data/                # SQLite DB, activity logs
```

### Implementation Order

1. **Credentials store** — `~/.deck/credentials.yaml` read/write, value resolution (literal/env/shell), `0600` permissions
2. **Config layering** — project config + user config merge with project-wins precedence
3. **Credential injection** — spawner reads provider from config, maps to env var, injects into sandbox
4. **`deck auth`** — add, remove, list, refresh, test subcommands
5. **`deck init`** — interactive wizard with charmbracelet/huh
6. **Daytona sandbox setup** — clone/fetch, branch checkout, credential injection
7. **`deck snapshot`** — create, update, staleness detection
