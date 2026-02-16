# Deck — Terminal-Native Agentic Workflow Environment

**Date:** 2026-02-16
**Status:** MVP Design — Approved
**Author:** SynDG + Claude

## Problem

Developers using AI coding agents (Claude Code, Codex, OpenCode) spend most of their time in the terminal but lack a unified environment for their workflow. The typical flow — plan, generate, review in a separate IDE, run review commands, commit, push, create PR — involves constant context switching. Code gets pushed without review ("blind push" problem). Attention splits across multiple tools and windows.

## Solution

**Deck** is a terminal-native environment for agentic coding workflows. It embeds agent sessions, provides an inline diff reviewer, and offers configurable commands — all in a single keyboard-driven TUI. Think Neovim, but for orchestrating AI agents.

## Target Audience

Solo developers using terminal-based AI coding agents. Agent-agnostic — works with Claude Code, Codex, OpenCode, or any terminal-based tool.

## Tech Stack

- **Language:** Go
- **TUI Framework:** Bubble Tea (charmbracelet/bubbletea)
- **Styling:** Lip Gloss (charmbracelet/lipgloss)
- **Components:** Bubbles (charmbracelet/bubbles)
- **Terminal Emulation:** charmbracelet/x/vt + creack/pty
- **Distribution:** npm (platform-specific binaries), Homebrew, GitHub Releases, go install

---

## Architecture

### UX Model: Focused View + Overlays

The agent terminal session is the default fullscreen view. All other views (diff, command palette, session switcher, project switcher) appear as floating overlays triggered by keybinds. Any overlay can be toggled to fullscreen.

This maximizes terminal space for the agent (where users spend most time) while keeping everything one keybind away.

### Keybindings: Vim-Style Modal

- **Insert mode (default on session focus):** All keystrokes pass through to the active PTY. The user is talking to the agent.
- **Normal mode (`Esc`):** Keys are handled by Deck. Navigate views, trigger overlays, run commands.
- **`i` to re-enter insert mode** and resume agent interaction.

Normal mode keybinds (configurable):

| Key | Action |
|-----|--------|
| `d` | Toggle diff view overlay |
| `f` | Toggle fullscreen on current overlay |
| `<leader>+s` | Session switcher (fuzzy) |
| `<leader>+p` | Project switcher (fuzzy) |
| `<leader>+n` | New session |
| `<leader>+c` | Command palette |
| `<leader>+o` | Open another project |
| `j/k` | Navigate within overlays |
| `Ctrl+U/D` | Scroll terminal in normal mode |
| `q` | Dismiss current overlay |
| `i` | Enter insert mode |
| `Esc` | Enter normal mode / dismiss overlay |

Default leader key: `Space` (configurable).

---

## Core Features (MVP)

### 1. Embedded Terminal Sessions (PTY)

Each session spawns an embedded pseudo-terminal:

- PTY creation via `creack/pty`
- Terminal emulation via `charmbracelet/x/vt` — handles ANSI parsing, screen buffer, alternate screen, cursor positioning, SGR styling
- Virtual terminal buffer rendered into Bubble Tea's `View()` each frame
- Input routing: insert mode forwards all keystrokes to PTY, normal mode captures them
- Resize handling: SIGWINCH sent to PTY when TUI or pane resizes
- Scrollback: configurable history (default 5000 lines), scrollable in normal mode
- Default command per project is configurable (defaults to `$SHELL`)

```yaml
# .deck/config.yaml
default_shell: "claude"
sessions:
  scrollback: 5000
```

### 2. Multi-Session & Multi-Project Management

**Sessions within a project:**
- Create new sessions (`<leader>+n`), each with a name (e.g., "auth feature", "bugfix")
- Switch between sessions via fuzzy picker overlay (`<leader>+s`)
- Each session is an independent embedded PTY

**Multi-project:**
- Launch with `deck ~/project-a`, add more from within (`<leader>+o`)
- Project switcher overlay (`<leader>+p`) for jumping between projects
- Each project has its own set of sessions and config

**Persistence:**
- Session layout (names, order, active session) persists in `.deck/sessions.yaml`
- On reopen, sessions restore with their config (agent history depends on the agent's own persistence)

### 3. Diff View with Hunk-Level Actions

Overlay diff viewer triggered by `d` in normal mode:

- Opens as centered overlay (~80% screen), fullscreen toggleable with `f`
- **Left panel:** file list with status indicators (`M` modified, `A` added, `D` deleted, `?` untracked)
- **Right panel:** unified diff for selected file, syntax-highlighted
- Diff source: `git diff` (unstaged) and `git diff --cached` (staged), parsed from unified diff format

**Navigation:**
- `j/k` — move between files
- `Tab` — switch focus between file list and diff panel
- `n/N` — jump between hunks within a file

**Hunk actions:**
- `s` — stage hunk
- `u` — unstage hunk
- `x` — discard hunk (with confirmation prompt)
- `S` — stage entire file
- `X` — discard entire file (with confirmation)

View refreshes immediately after each action. Color scheme: green additions, red deletions, dim context (themeable).

### 4. Project-Scoped Command System

Commands are YAML files in `.deck/commands/`:

```yaml
# .deck/commands/review.yaml
name: "Review Changes"
description: "Run AI code review on staged changes"
key: "R"                    # optional direct keybind in normal mode
cmd: |
  codex -e "Review these changes: {{staged_diff}}"
output: overlay             # overlay | fullscreen | session
```

**Context variables injected at runtime:**
- `{{project_path}}`, `{{project_name}}`
- `{{current_branch}}`, `{{default_branch}}`
- `{{staged_diff}}`, `{{unstaged_diff}}`
- `{{changed_files}}`, `{{staged_files}}`
- `{{session_name}}`

**Output modes:**
- `overlay` — command output in a floating panel, dismissable
- `fullscreen` — takes over screen while running
- `session` — spawns a new named session (for long-running tasks)

**Command palette:** `<leader>+c` opens a Telescope-style fuzzy picker listing all available commands.

**Builtin defaults:** Starter commands ship in `~/.config/deck/defaults/` and get copied to new projects on `deck init`. Users edit or delete freely.

### 5. Status Bar

Persistent bar at the bottom of the screen:

```
[NORMAL] deck | project: my-app | session: auth-feature | main | 3M 1A | 4 staged
```

Shows: current mode, tool name, project, active session, branch, change summary, staged count.

### 6. Configuration

**Global config:** `~/.config/deck/config.yaml`
- Default keybinds, theme, default shell, scrollback size
- Default commands template directory

**Project config:** `.deck/config.yaml`
- Project-specific overrides (default shell, custom keybinds)
- Session persistence: `.deck/sessions.yaml`
- Commands: `.deck/commands/*.yaml`

---

## Distribution

| Channel | Command |
|---------|---------|
| npm | `npm install -g @syndg/deck` / `bun install -g @syndg/deck` |
| Homebrew | `brew install syndg/tap/deck` |
| GitHub Releases | Direct binary download (darwin-arm64, darwin-amd64, linux-arm64, linux-amd64) |
| go install | `go install github.com/syndg/deck@latest` |

npm distribution follows the esbuild pattern: platform-specific binary packages (`@syndg/deck-darwin-arm64`, etc.) with a main package that detects platform and installs the correct binary.

---

## Deferred (Post-MVP)

| Feature | Rationale |
|---------|-----------|
| Multi-agent orchestration | Complex IPC, unclear UX. MVP supports multiple manual sessions |
| Line-level diff editing | Essentially a text editor. Hunk-level covers 90% of needs |
| Workflow chains (cmd → cmd → cmd) | Shell `&&` in command definitions covers this for now |
| Go/Lua/WASM plugin API | Context variables cover MVP needs. Real plugin hooks need usage data |
| Custom themes | Ship one good dark theme + respect terminal colors |
| Remote sessions (SSH) | Different PTY model, big scope |
| Image/rich content (Sixel, Kitty) | Rabbit hole, text-first for MVP |
| Collaborative/multiplayer | Out of scope |

---

## Project Structure (Proposed)

```
deck/
├── cmd/
│   └── deck/
│       └── main.go              # CLI entry point
├── internal/
│   ├── app/
│   │   ├── app.go               # Root Bubble Tea model
│   │   ├── keymap.go            # Keybinding definitions
│   │   └── mode.go              # Modal state (normal/insert)
│   ├── session/
│   │   ├── session.go           # Session model (PTY + vt)
│   │   ├── manager.go           # Multi-session management
│   │   └── persist.go           # Session persistence
│   ├── project/
│   │   ├── project.go           # Project model
│   │   └── manager.go           # Multi-project management
│   ├── diff/
│   │   ├── diff.go              # Diff view model
│   │   ├── parser.go            # Unified diff parser
│   │   └── hunk.go              # Hunk actions (stage/unstage/discard)
│   ├── commands/
│   │   ├── command.go           # Command model
│   │   ├── loader.go            # YAML loader from .deck/commands/
│   │   ├── context.go           # Context variable injection
│   │   └── palette.go           # Command palette overlay
│   ├── overlay/
│   │   ├── overlay.go           # Overlay rendering system
│   │   └── compositor.go        # Cell-by-cell overlay compositing
│   ├── statusbar/
│   │   └── statusbar.go         # Status bar component
│   └── config/
│       ├── config.go            # Config loading (global + project)
│       └── defaults.go          # Default config/commands
├── pkg/
│   └── terminal/
│       ├── pty.go               # PTY wrapper (creack/pty)
│       └── vt.go                # Virtual terminal wrapper (x/vt)
├── configs/
│   └── defaults/
│       ├── config.yaml          # Default global config
│       └── commands/
│           ├── commit.yaml
│           ├── push.yaml
│           └── test.yaml
├── go.mod
├── go.sum
└── README.md
```

---

## Summary

Deck is a terminal-native command center for developers working with AI coding agents. It embeds agent sessions directly, provides inline code review with hunk-level control, and automates repetitive workflows through project-scoped commands. Vim-style modal keybindings keep hands on the keyboard. One tool, one window, full control.
