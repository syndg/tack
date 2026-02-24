# Deck — Technical Design Document

**Date:** 2026-02-16
**Status:** Implementation Blueprint
**Prerequisite:** [MVP Design Doc](plans/2026-02-16-deck-design.md)

---

## Table of Contents

1. [System Overview](#system-overview)
2. [Module Design](#module-design)
3. [PTY + VT Integration](#pty--vt-integration)
4. [Overlay Compositing](#overlay-compositing)
5. [Diff Engine](#diff-engine)
6. [Command System](#command-system)
7. [State Management](#state-management)
8. [Configuration Loading](#configuration-loading)
9. [Session Persistence](#session-persistence)
10. [Distribution](#distribution)

---

## System Overview

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    tea.Program (main loop)                   │
│  ┌───────────────────────────────────────────────────────┐  │
│  │                  app.Model (root)                     │  │
│  │                                                       │  │
│  │  ┌─────────┐  ┌────────────┐  ┌──────────────────┐  │  │
│  │  │  Mode   │  │  Keymap    │  │  Active Overlay   │  │  │
│  │  │ (n/i)   │  │ (dispatch) │  │  (optional)       │  │  │
│  │  └─────────┘  └────────────┘  └──────────────────┘  │  │
│  │                                                       │  │
│  │  ┌───────────────────────────────────────────────┐   │  │
│  │  │           project.Manager                     │   │  │
│  │  │  ┌─────────────────────────────────────────┐  │   │  │
│  │  │  │         project.Project                 │  │   │  │
│  │  │  │  ┌─────────────────────────────────┐    │  │   │  │
│  │  │  │  │      session.Manager            │    │  │   │  │
│  │  │  │  │  ┌───────────────────────────┐  │    │  │   │  │
│  │  │  │  │  │    session.Session        │  │    │  │   │  │
│  │  │  │  │  │  ┌─────────┐ ┌─────────┐ │  │    │  │   │  │
│  │  │  │  │  │  │ PTY     │ │ VT      │ │  │    │  │   │  │
│  │  │  │  │  │  │(creack) │ │(x/vt)   │ │  │    │  │   │  │
│  │  │  │  │  │  └────┬────┘ └────┬────┘ │  │    │  │   │  │
│  │  │  │  │  │       │ stdout    │render │  │    │  │   │  │
│  │  │  │  │  │       └───────────┘      │  │    │  │   │  │
│  │  │  │  │  └───────────────────────────┘  │    │  │   │  │
│  │  │  │  └─────────────────────────────────┘    │  │   │  │
│  │  │  └─────────────────────────────────────────┘  │   │  │
│  │  └───────────────────────────────────────────────┘   │  │
│  │                                                       │  │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌─────────┐ │  │
│  │  │ Overlay  │ │ Diff     │ │ Commands │ │ Status  │ │  │
│  │  │ System   │ │ View     │ │ Palette  │ │ Bar     │ │  │
│  │  └──────────┘ └──────────┘ └──────────┘ └─────────┘ │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Data Flow

```
User Keystroke
     │
     ▼
tea.KeyMsg ──► app.Update()
     │
     ├─ Mode == Insert? ──► Forward to PTY stdin (session.WriteInput)
     │
     └─ Mode == Normal? ──► Keymap dispatch
                │
                ├─ 'i'         → set Mode=Insert
                ├─ 'd'         → toggle DiffOverlay
                ├─ '<leader>s' → show SessionPicker
                ├─ '<leader>c' → show CommandPalette
                ├─ 'j/k'      → delegate to active overlay
                ├─ 'Ctrl+U/D' → scroll terminal buffer
                └─ 'q'         → dismiss overlay

PTY stdout ──► goroutine reads ──► writes to vt.Terminal
                                        │
                                        ▼
                                 tea.Program.Send(TerminalOutputMsg)
                                        │
                                        ▼
                                 app.Update() → triggers View()
                                        │
                                        ▼
                                 View() reads VT screen buffer
                                        │
                                 Overlay compositor layers overlay on top
                                        │
                                        ▼
                                 Status bar appended at bottom
                                        │
                                        ▼
                                 Final string → Bubble Tea renderer
```

### Core Dependencies

| Package | Import Path | Purpose |
|---------|------------|---------|
| Bubble Tea | `github.com/charmbracelet/bubbletea` | TUI framework (Elm architecture) |
| Lip Gloss | `github.com/charmbracelet/lipgloss` | Styling (borders, colors, layout) |
| Bubbles | `github.com/charmbracelet/bubbles` | Pre-built components (list, viewport, textinput, help, key) |
| x/vt | `github.com/charmbracelet/x/vt` | Virtual terminal emulator (ANSI parsing, screen buffer). Uses `Emulator`/`SafeEmulator` types. **Note:** Does NOT include scrollback — we implement history buffer separately. |
| x/ansi | `github.com/charmbracelet/x/ansi` | ANSI sequence parsing primitives |
| ultraviolet | `github.com/charmbracelet/ultraviolet` | Cell/screen buffer types (used internally by x/vt) |
| creack/pty | `github.com/creack/pty` | PTY creation and management |
| go-yaml | `gopkg.in/yaml.v3` | YAML parsing for config/commands |
| go-diff | `github.com/sourcegraph/go-diff` | Unified diff parsing (optional, may parse manually) |

---

## Module Design

### `internal/app` — Root Application Model

**Responsibility:** Top-level Bubble Tea model. Owns the mode state, keymap, active overlay, and delegates to sub-models.

**Key Types:**

```go
// Mode represents the vim-style input mode.
type Mode int

const (
    ModeNormal Mode = iota
    ModeInsert
)

// Model is the root Bubble Tea model.
type Model struct {
    mode           Mode
    keymap         Keymap
    projectMgr     *project.Manager
    activeOverlay  overlay.Overlay     // nil when no overlay shown
    statusBar      statusbar.Model
    width, height  int
    quitting       bool
}

// Keymap holds configurable key bindings.
type Keymap struct {
    Leader         string              // default: " " (space)
    ToggleDiff     key.Binding
    SessionPicker  key.Binding         // leader sequence
    ProjectPicker  key.Binding
    NewSession     key.Binding
    CommandPalette key.Binding
    OpenProject    key.Binding
    ScrollUp       key.Binding
    ScrollDown     key.Binding
    DismissOverlay key.Binding
    EnterInsert    key.Binding
}
```

**Messages Produced:**
- `ModeChangedMsg` — broadcast when mode switches
- `QuitMsg` — triggers graceful shutdown

**Messages Consumed:**
- `tea.KeyMsg` — all key events
- `tea.WindowSizeMsg` — terminal resize
- `TerminalOutputMsg` — from PTY goroutine
- `OverlayActionMsg` — from overlay sub-models
- `DiffRefreshMsg` — diff data updated

**Dependencies:** All other internal packages.

**Critical Details:**
- Update() must check mode FIRST before dispatching keys
- In Insert mode, ALL key events (including Esc) pass to PTY except the configured escape sequence
- Leader key sequences: accumulate in a buffer with a timeout (e.g., 300ms). If leader pressed, wait for second key. If timeout, discard.
- The root model's View() is the compositor entry point — it renders the base terminal, then layers overlays on top.

---

### `internal/session` — Terminal Session

**Responsibility:** Manages a single embedded terminal session (PTY + VT). Handles input forwarding, output reading, and resize.

**Key Types:**

```go
// Session represents one embedded terminal session.
type Session struct {
    ID        string
    Name      string
    Command   string                // shell command that launched this session
    WorkDir   string
    pty       *terminal.PTY        // from pkg/terminal
    vt        *terminal.VT         // from pkg/terminal
    scrollOff int                   // scroll offset (0 = bottom)
    width     int
    height    int
    exited    bool
}

// Manager manages multiple sessions within a project.
type Manager struct {
    sessions  []*Session
    active    int                   // index of active session
    projectID string
}
```

**Messages Produced:**
- `TerminalOutputMsg{SessionID string}` — signals new PTY output available
- `SessionExitedMsg{SessionID string, ExitCode int}` — process exited

**Messages Consumed:**
- `tea.KeyMsg` (in insert mode, forwarded from app)
- `tea.WindowSizeMsg` (resize propagation)

**Dependencies:** `pkg/terminal`

**Critical Details:**
- Each session spawns a goroutine that reads PTY stdout and writes to the VT. After each write, it sends `TerminalOutputMsg` to the tea.Program via `p.Send()`.
- The goroutine reads in a loop with a small buffer (4KB). Data is written to VT synchronously (VT is not thread-safe — lock or channel-serialize writes).
- `View()` reads the VT screen buffer and converts it to a lipgloss-styled string. Only the active session's View is rendered.
- Scroll offset: in normal mode, Ctrl+U/D adjusts `scrollOff`. View() reads from `(totalLines - height - scrollOff)` to render the visible window.
- On resize: call `pty.Setsize()` and `vt.Resize()` together.

---

### `internal/session/persist.go` — Session Persistence

**Responsibility:** Save and restore session layout to `.deck/sessions.yaml`.

**Key Types:**

```go
// SessionLayout represents a serializable session configuration.
type SessionLayout struct {
    Name    string `yaml:"name"`
    Command string `yaml:"command"`
    WorkDir string `yaml:"workdir,omitempty"`
}

// LayoutFile is the top-level sessions.yaml structure.
type LayoutFile struct {
    Active   string          `yaml:"active"`   // name of the active session
    Sessions []SessionLayout `yaml:"sessions"`
}
```

**Critical Details:**
- Does NOT persist terminal content (scrollback, screen state). Only persists session metadata.
- Terminal history restoration depends on the agent's own persistence (e.g., Claude Code restores its conversation on restart).
- Save on: session create, session delete, session switch, app exit.
- Restore on: app startup — recreate sessions with their commands, mark the previously active one as active.

---

### `internal/project` — Project Management

**Responsibility:** Represents a project (directory) and its associated sessions, config, and commands.

**Key Types:**

```go
// Project represents a single project directory.
type Project struct {
    ID         string
    Name       string              // directory basename
    Path       string              // absolute path
    Config     *config.ProjectConfig
    SessionMgr *session.Manager
    Commands   []*commands.Command
}

// Manager manages multiple projects.
type Manager struct {
    projects []*Project
    active   int                   // index of active project
}
```

**Messages Produced:**
- `ProjectSwitchedMsg{ProjectID string}`
- `ProjectAddedMsg{ProjectID string}`

**Messages Consumed:**
- Overlay actions from session/project pickers

**Dependencies:** `internal/session`, `internal/config`, `internal/commands`

**Critical Details:**
- First project comes from CLI arg (`deck ~/my-project`). Additional projects added via `<leader>+o` which opens a directory picker or accepts a path.
- Each project loads its own `.deck/config.yaml` and `.deck/commands/*.yaml` on addition.
- Project switching: pause rendering of old project's active session, activate new project's active session.

---

### `internal/diff` — Diff View

**Responsibility:** Parse unified diffs, render the diff overlay, handle hunk-level actions (stage/unstage/discard).

**Key Types:**

```go
// FileDiff represents changes to a single file.
type FileDiff struct {
    Path      string
    Status    FileStatus            // Modified, Added, Deleted, Untracked
    IsStaged  bool
    Hunks     []Hunk
}

type FileStatus int

const (
    StatusModified FileStatus = iota
    StatusAdded
    StatusDeleted
    StatusUntracked
    StatusRenamed
)

// Hunk represents a single diff hunk.
type Hunk struct {
    OldStart int
    OldCount int
    NewStart int
    NewCount int
    Header   string                // e.g., "@@ -10,7 +10,8 @@ func main()"
    Lines    []DiffLine
}

// DiffLine represents a single line within a hunk.
type DiffLine struct {
    Type    LineType               // Context, Add, Delete
    Content string
}

type LineType int

const (
    LineContext LineType = iota
    LineAdd
    LineDelete
)

// Model is the Bubble Tea model for the diff overlay.
type Model struct {
    files       []FileDiff
    fileIdx     int                // selected file index
    hunkIdx     int                // selected hunk index
    focusPanel  Panel              // FileList or DiffContent
    viewport    viewport.Model     // for scrolling diff content
    width       int
    height      int
}

type Panel int

const (
    PanelFileList Panel = iota
    PanelDiff
)
```

**Messages Produced:**
- `DiffRefreshMsg` — after staging/unstaging/discarding, re-fetch diff
- `OverlayDismissMsg` — user pressed q

**Messages Consumed:**
- `tea.KeyMsg` — j/k navigation, s/u/x hunk actions, Tab focus switch, n/N hunk jump

**Dependencies:** `os/exec` (for git commands)

**Critical Details:**

**Diff Fetching:**
- Unstaged: `git diff --no-color --unified=3`
- Staged: `git diff --cached --no-color --unified=3`
- Untracked: `git ls-files --others --exclude-standard`
- Run these from the project's working directory.
- Combine into a unified file list showing all changes.

**Hunk Staging (lazygit pattern):**
- Stage a hunk: extract hunk as a patch (with file header), pipe to `git apply --cached --unidiff-zero -`
- Unstage a hunk: extract hunk, pipe to `git apply --cached --reverse --unidiff-zero -`
- Discard a hunk: extract hunk, pipe to `git apply --reverse --unidiff-zero -` (applies to working tree)
- Stage entire file: `git add <path>`
- Discard entire file: `git checkout -- <path>` (for tracked) or `rm <path>` (for untracked, with confirmation)

**Patch Construction:**
A valid patch for `git apply` requires:
```
diff --git a/<path> b/<path>
--- a/<path>
+++ b/<path>
@@ -<old_start>,<old_count> +<new_start>,<new_count> @@
 context line
-deleted line
+added line
 context line
```

The hunk must include the file header and the hunk header. The `--unidiff-zero` flag allows zero-context patches.

**Rendering:**
- File list panel: ~30% width on left. Each file shows status icon and path.
- Diff panel: ~70% width on right. Unified diff with syntax highlighting.
- Colors: green for additions, red for deletions, dim gray for context, bright for selected hunk.
- Selected hunk gets a distinct background or left-border indicator.

---

### `internal/commands` — Command System

**Responsibility:** Load command definitions from YAML, resolve context variables, execute commands, and display output.

**Key Types:**

```go
// Command represents a user-defined command.
type Command struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description,omitempty"`
    Key         string   `yaml:"key,omitempty"`      // direct keybind (normal mode)
    Cmd         string   `yaml:"cmd"`                // shell command template
    Output      string   `yaml:"output,omitempty"`   // "overlay" | "fullscreen" | "session"
    Confirm     bool     `yaml:"confirm,omitempty"`  // require confirmation before running
    Shell       string   `yaml:"shell,omitempty"`    // override shell (default: sh -c)
}

// Context holds runtime variables for template resolution.
type Context struct {
    ProjectPath   string
    ProjectName   string
    CurrentBranch string
    DefaultBranch string
    StagedDiff    func() string    // lazy — only called if template uses it
    UnstagedDiff  func() string    // lazy
    ChangedFiles  func() string    // lazy
    StagedFiles   func() string    // lazy
    SessionName   string
}

// Loader handles discovering and parsing command YAML files.
type Loader struct {
    projectDir string
    globalDir  string
}

// PaletteModel is the Bubble Tea model for the command picker overlay.
type PaletteModel struct {
    list     list.Model            // bubbles/list with filtering
    commands []*Command
    width    int
    height   int
}
```

**Messages Produced:**
- `CommandExecuteMsg{Command *Command, Output string}` — command selected for execution
- `CommandOutputMsg{Content string}` — command output received

**Messages Consumed:**
- `tea.KeyMsg` — navigation and selection within palette
- Direct keybinds from normal mode (if command has `key` field)

**Dependencies:** `os/exec`, `text/template` or `strings.Replacer`

**Critical Details:**

**Context Variable Resolution:**
- Variables use `{{variable_name}}` syntax.
- Use Go's `text/template` for resolution. Template data is the Context struct.
- **Lazy evaluation is critical:** `{{staged_diff}}` should only invoke `git diff --cached` if the template actually references it. Implement by using function fields in Context — the template calls the function which executes the git command on first call, caching the result.
- Template rendering happens just before execution.

**Output Modes:**
- `overlay` (default): Command stdout/stderr captured, displayed in a scrollable overlay panel. Dismissed with `q`.
- `fullscreen`: Overlay takes full screen while command runs.
- `session`: Creates a new named session, runs the command in its PTY. User can interact with it. Good for long-running or interactive commands.

**YAML Schema:**
```yaml
name: "Review Changes"            # required, shown in palette
description: "AI code review"     # optional, shown as subtitle
key: "R"                          # optional, direct keybind in normal mode
cmd: |                            # required, shell command (supports templates)
  claude "Review: {{staged_diff}}"
output: overlay                   # optional, default "overlay"
confirm: false                    # optional, default false
shell: "sh -c"                    # optional, override execution shell
```

---

### `internal/overlay` — Overlay Rendering System

**Responsibility:** Manages overlay lifecycle (show/dismiss/fullscreen) and provides the cell-level compositor that layers overlay content on top of the base terminal view.

**Key Types:**

```go
// Overlay is the interface all overlay types implement.
type Overlay interface {
    tea.Model                      // Init, Update, View
    ID() string                    // unique identifier
    Title() string                 // shown in overlay header
    Fullscreen() bool              // current fullscreen state
    SetFullscreen(bool)
    SetSize(width, height int)
}

// Compositor handles layering overlay on top of base content.
type Compositor struct {
    width, height int
}

// CompositeResult is the final rendered string.
type CompositeResult struct {
    Content string
}
```

**Messages Produced:**
- `OverlayDismissMsg{}` — overlay wants to close
- `OverlayFullscreenToggleMsg{}` — toggle fullscreen

**Messages Consumed:**
- `tea.KeyMsg` — `q` to dismiss, `f` to toggle fullscreen
- `tea.WindowSizeMsg` — resize overlay

**Dependencies:** `lipgloss`

**Critical Details:** See [Overlay Compositing](#overlay-compositing) section below.

---

### `internal/statusbar` — Status Bar

**Responsibility:** Render the persistent status bar at the bottom of the screen.

**Key Types:**

```go
// Model is the status bar Bubble Tea model.
type Model struct {
    mode       app.Mode
    project    string
    session    string
    branch     string
    changes    ChangeStats
    staged     int
    width      int
}

// ChangeStats holds counts of file change types.
type ChangeStats struct {
    Modified  int
    Added     int
    Deleted   int
    Untracked int
}
```

**Messages Consumed:**
- `ModeChangedMsg` — update mode indicator
- `ProjectSwitchedMsg` — update project name
- `SessionSwitchedMsg` — update session name
- `DiffRefreshMsg` — update change counts
- `GitBranchMsg` — update branch name

**Critical Details:**
- Renders as a single line at the bottom of the screen.
- Format: `[NORMAL] deck | project: X | session: Y | main | 3M 1A | 4 staged`
- Mode indicator: `[NORMAL]` in bold yellow, `[INSERT]` in bold green.
- Width is the full terminal width. Content is truncated if too long.
- Refresh git stats on: diff overlay open/close, command execution, focus return.
- Git stats come from `git status --porcelain` and `git diff --cached --stat`.

---

### `internal/config` — Configuration

**Responsibility:** Load, merge, and provide configuration from global and project-level YAML files.

**Key Types:**

```go
// Config is the merged configuration.
type Config struct {
    DefaultShell string        `yaml:"default_shell"`
    Leader       string        `yaml:"leader"`
    Scrollback   int           `yaml:"scrollback"`
    Theme        ThemeConfig   `yaml:"theme"`
    Keybinds     KeybindConfig `yaml:"keybinds,omitempty"`
}

// ThemeConfig holds color/style configuration.
type ThemeConfig struct {
    DiffAdd     string `yaml:"diff_add"`      // hex color
    DiffDelete  string `yaml:"diff_delete"`
    DiffContext string `yaml:"diff_context"`
    StatusBar   string `yaml:"status_bar"`
    Overlay     string `yaml:"overlay_border"`
}

// KeybindConfig allows overriding default keybinds.
type KeybindConfig struct {
    ToggleDiff     string `yaml:"toggle_diff,omitempty"`
    SessionPicker  string `yaml:"session_picker,omitempty"`
    ProjectPicker  string `yaml:"project_picker,omitempty"`
    NewSession     string `yaml:"new_session,omitempty"`
    CommandPalette string `yaml:"command_palette,omitempty"`
}
```

**Critical Details:**

**Merge Strategy:**
1. Hard-coded defaults (always present, sensible values)
2. Global config `~/.config/deck/config.yaml` overrides defaults
3. Project config `.deck/config.yaml` overrides global

Merge is field-level: a project config that only sets `default_shell` inherits everything else from global/defaults. Use a simple approach: unmarshal defaults, then unmarshal global on top, then project on top. Go's yaml.v3 zero-value behavior makes this straightforward.

**Default Values:**
```yaml
default_shell: ""                  # empty = $SHELL
leader: " "                        # space
scrollback: 5000
theme:
  diff_add: "#a6e3a1"
  diff_delete: "#f38ba8"
  diff_context: "#585b70"
  status_bar: "#1e1e2e"
  overlay_border: "#89b4fa"
```

---

### `pkg/terminal` — PTY and VT Wrappers

**Responsibility:** Thin wrappers around `creack/pty` and `charmbracelet/x/vt` that provide a clean interface for the session layer.

**Key Types:**

```go
// PTY wraps a pseudo-terminal created by creack/pty.
type PTY struct {
    file    *os.File               // the PTY master file descriptor
    cmd     *exec.Cmd              // the running command
    done    chan struct{}           // closed when process exits
}

// NewPTY starts a command in a new pseudo-terminal.
func NewPTY(command string, args []string, workDir string, rows, cols int) (*PTY, error)

// Write sends input to the PTY (stdin of the child process).
func (p *PTY) Write(data []byte) (int, error)

// Read reads output from the PTY (stdout of the child process).
func (p *PTY) Read(buf []byte) (int, error)

// Resize changes the PTY window size. Sends SIGWINCH to child.
func (p *PTY) Resize(rows, cols int) error

// Close terminates the PTY and the child process.
func (p *PTY) Close() error

// Done returns a channel that closes when the child process exits.
func (p *PTY) Done() <-chan struct{}

// VT wraps charmbracelet/x/vt SafeEmulator with scrollback history.
type VT struct {
    emu        *vt.SafeEmulator   // Thread-safe terminal emulator
    history    *RingBuffer        // Scrollback history (custom ring buffer)
    scrollback int                // Max scrollback lines
    altScreen  bool               // True when alternate screen active (no scrollback)
}

// NewVT creates a virtual terminal with the given dimensions.
func NewVT(width, height, scrollback int) *VT

// Write processes raw PTY output through the terminal emulator.
// Captures lines scrolling out of view into history buffer.
func (v *VT) Write(data []byte) (int, error)

// Resize changes the virtual terminal dimensions.
func (v *VT) Resize(width, height int)

// Render returns the current screen content as a styled string.
// scrollOffset=0 means the bottom of the buffer (most recent output).
// Positive offset scrolls back into history.
func (v *VT) Render(scrollOffset int) string

// TotalLines returns the total number of lines including scrollback.
func (v *VT) TotalLines() int

// RingBuffer is a fixed-size circular buffer for scrollback history.
type RingBuffer struct {
    lines    [][]Cell
    capacity int
    head     int                  // Next write position
    count    int                  // Current number of lines stored
}
```

**Dependencies:** `creack/pty`, `charmbracelet/x/vt`, `charmbracelet/ultraviolet` (used by x/vt for Cell type), `lipgloss` (for styled rendering)

**Critical Details:** See [PTY + VT Integration](#pty--vt-integration) section below.

---

## PTY + VT Integration

This is the most critical subsystem. It's how Deck embeds a fully functional terminal within Bubble Tea.

### Architecture

```
┌──────────────┐         ┌──────────────┐         ┌──────────────┐
│   creack/pty │         │    x/vt      │         │  Bubble Tea  │
│              │  stdout │  Terminal    │  cells  │   View()     │
│  Child Proc  ├────────►│  (ANSI      ├────────►│  (string     │
│  (e.g. bash, │         │   parser +  │         │   output)    │
│   claude)    │◄────────┤   screen    │         │              │
│              │  stdin  │   buffer)   │         │              │
└──────────────┘         └──────────────┘         └──────────────┘
```

### PTY Creation (creack/pty)

```go
func NewPTY(command string, args []string, workDir string, rows, cols int) (*PTY, error) {
    cmd := exec.Command(command, args...)
    cmd.Dir = workDir
    cmd.Env = append(os.Environ(),
        fmt.Sprintf("TERM=%s", os.Getenv("TERM")),
        fmt.Sprintf("COLUMNS=%d", cols),
        fmt.Sprintf("LINES=%d", rows),
    )

    winsize := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
    ptmx, err := pty.StartWithSize(cmd, winsize)
    if err != nil {
        return nil, fmt.Errorf("pty start: %w", err)
    }

    return &PTY{file: ptmx, cmd: cmd, done: make(chan struct{})}, nil
}
```

Key points:
- `pty.StartWithSize` creates the PTY with initial dimensions, so the child process sees correct `COLUMNS`/`LINES` from the start.
- The `TERM` env var is passed through so the child process knows terminal capabilities.
- The PTY master (`ptmx`) is a regular `*os.File` — read for stdout, write for stdin.

### VT Processing (x/vt)

The virtual terminal emulator parses ANSI escape sequences from PTY output and maintains an in-memory screen buffer. **Important:** x/vt does NOT include built-in scrollback — we implement a history buffer wrapper.

```go
// VT wraps charmbracelet/x/vt with a scrollback history buffer.
type VT struct {
    emu        *vt.SafeEmulator  // Thread-safe terminal emulator
    history    [][]vt.Cell       // Scrollback buffer (ring buffer)
    historyIdx int               // Current write position in ring buffer
    scrollback int               // Max scrollback lines
    mu         sync.Mutex
}

func NewVT(width, height, scrollback int) *VT {
    return &VT{
        emu:        vt.NewSafeEmulator(width, height),
        history:    make([][]vt.Cell, scrollback),
        scrollback: scrollback,
    }
}

func (v *VT) Write(data []byte) (int, error) {
    // Before writing, capture lines that will scroll out of view
    v.captureScrolledLines()
    return v.emu.Write(data)
}
```

**Scrollback Implementation Strategy:**
1. Before each `Write()`, check if content will scroll
2. Copy lines that scroll off the top into the history ring buffer
3. When rendering with scroll offset, combine history + visible screen
4. History stored as `[][]Cell` (ring buffer) to avoid constant reallocation

The x/vt `SafeEmulator` handles:
- **Cursor positioning** (CSI H, CSI A/B/C/D)
- **SGR styling** (bold, italic, colors, 256-color, truecolor)
- **Screen operations** (clear, scroll, insert/delete lines)
- **Alternate screen buffer** (used by vim, less, etc.) — when active, scrollback is paused
- **Damage tracking** (`Touched()` returns changed lines for efficient re-rendering)

### Output Reading Goroutine

Each session spawns a goroutine that bridges PTY output to the VT and Bubble Tea:

```go
func (s *Session) readLoop(program *tea.Program) {
    buf := make([]byte, 4096)
    for {
        n, err := s.pty.Read(buf)
        if err != nil {
            program.Send(SessionExitedMsg{SessionID: s.ID})
            return
        }
        s.vt.Write(buf[:n])
        program.Send(TerminalOutputMsg{SessionID: s.ID})
    }
}
```

The `program.Send()` call injects a message into Bubble Tea's event loop, which triggers `Update()` → `View()` → re-render. This is how the terminal display stays live.

**Important:** `program.Send()` is thread-safe and can be called from any goroutine. It's the only way to communicate from background goroutines to the Bubble Tea loop.

### Rendering the VT Buffer

The `Render()` method converts the VT's cell grid into a string with ANSI styling. **x/vt provides `Render()` directly which returns an ANSI-styled string:**

```go
func (v *VT) Render(scrollOffset int) string {
    // If no scroll offset and not viewing history, use built-in Render()
    if scrollOffset == 0 && !v.viewingHistory() {
        return v.emu.Render()  // x/vt's built-in render with ANSI codes
    }

    // When scrolling into history, we need to composite:
    // 1. Lines from history buffer
    // 2. Lines from current screen (partial if offset > 0)
    return v.renderWithHistory(scrollOffset)
}

func (v *VT) renderWithHistory(scrollOffset int) string {
    var buf strings.Builder
    height := v.emu.Height()

    // Calculate which lines to show
    historyLines := v.history.Last(scrollOffset)  // Get N most recent history lines
    screenLines := v.getScreenLines()             // Current visible screen

    // Compose: history lines + screen lines, limited to height
    totalAvailable := len(historyLines) + len(screenLines)
    startIdx := totalAvailable - height - scrollOffset
    if startIdx < 0 {
        startIdx = 0
    }

    // Render each line with ANSI styling
    for i := 0; i < height; i++ {
        lineIdx := startIdx + i
        if lineIdx < len(historyLines) {
            buf.WriteString(renderLine(historyLines[lineIdx]))
        } else {
            screenIdx := lineIdx - len(historyLines)
            buf.WriteString(renderLine(screenLines[screenIdx]))
        }
        if i < height-1 {
            buf.WriteRune('\n')
        }
    }
    return buf.String()
}
```

**Key x/vt APIs for rendering:**
- `emu.Render()` — Returns full screen as ANSI-styled string (efficient for no-scroll case)
- `emu.String()` — Returns plain text (no ANSI codes)
- `emu.CellAt(x, y)` — Get individual cell with style info
- `emu.Touched()` — Returns only changed lines (for incremental rendering)

The x/vt library provides cell-by-cell access via `CellAt()` returning `*uv.Cell` (from ultraviolet) including character content, foreground/background colors, and text attributes.

### Input Routing

```
tea.KeyMsg arrives at app.Update()
     │
     ├── Mode == Insert
     │      │
     │      ├── Key == Esc → set Mode = Normal
     │      │
     │      └── Any other key → convert to bytes, write to session PTY
     │           session.WriteInput(keyToBytes(msg))
     │
     └── Mode == Normal
            │
            └── Dispatch via Keymap (see app.Keymap)
```

Converting `tea.KeyMsg` to bytes for PTY:
- Regular characters: UTF-8 encode the rune
- Control keys: map to control sequences (Ctrl+C → 0x03, Enter → 0x0D, etc.)
- Special keys: map to ANSI sequences (Arrow Up → `\x1b[A`, etc.)
- This conversion function is critical — any key the user presses in insert mode must arrive at the PTY exactly as if typed in a raw terminal.

### Resize Handling

```
tea.WindowSizeMsg arrives at app.Update()
     │
     ├── Update app.width, app.height
     ├── Calculate content area (full height - 1 for status bar)
     ├── Calculate session area (content area - overlay area if any)
     │
     └── For active session:
            ├── session.Resize(newWidth, newHeight)
            │     ├── pty.Resize(rows, cols)      // sends SIGWINCH to child
            │     └── vt.Resize(cols, rows)        // reflows VT buffer
            │
            └── For overlays: overlay.SetSize(newWidth, newHeight)
```

The `pty.Resize()` call uses `pty.Setsize()` from creack/pty, which issues `TIOCSWINSZ` ioctl on the PTY, causing the kernel to send SIGWINCH to the child process group.

---

## Overlay Compositing

### Approach: String-Based Layering

Rather than cell-by-cell compositing (complex, requires custom renderer), we use Bubble Tea's string-based rendering with lipgloss layout:

**When no overlay is active:**
```
View() = terminal_content + "\n" + status_bar
```

**When an overlay is active (non-fullscreen):**
```
View() = lipgloss.Place(
    width, height,
    lipgloss.Center, lipgloss.Center,
    overlayView,
    lipgloss.WithWhitespaceChars(" "),
    lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
) + "\n" + status_bar
```

This uses lipgloss's `Place` function to center the overlay. The background is filled with the terminal content underneath (or dimmed).

**Simpler approach (recommended for MVP):**
```go
func (m Model) View() string {
    // Always render the status bar
    statusLine := m.statusBar.View()

    // Calculate content height (total - status bar)
    contentHeight := m.height - 1

    if m.activeOverlay != nil {
        if m.activeOverlay.Fullscreen() {
            // Overlay takes all content space
            content := m.activeOverlay.View()
            return lipgloss.JoinVertical(lipgloss.Left, content, statusLine)
        }

        // Overlay as centered float
        // Render overlay with border
        overlayW := m.width * 80 / 100
        overlayH := contentHeight * 80 / 100
        m.activeOverlay.SetSize(overlayW, overlayH)

        overlayView := lipgloss.NewStyle().
            Border(lipgloss.RoundedBorder()).
            BorderForeground(lipgloss.Color("#89b4fa")).
            Width(overlayW).
            Height(overlayH).
            Render(m.activeOverlay.View())

        // Place overlay in center
        content := lipgloss.Place(
            m.width, contentHeight,
            lipgloss.Center, lipgloss.Center,
            overlayView,
        )
        return lipgloss.JoinVertical(lipgloss.Left, content, statusLine)
    }

    // No overlay — render terminal fullscreen
    session := m.projectMgr.ActiveSession()
    content := session.View()

    return lipgloss.JoinVertical(lipgloss.Left, content, statusLine)
}
```

### Overlay Lifecycle

1. Normal mode keybind triggers overlay → `m.activeOverlay = overlay.New(...)`
2. Keys in normal mode delegated to active overlay if present (j/k, n/N, Enter, etc.)
3. `f` toggles `overlay.SetFullscreen(!overlay.Fullscreen())`
4. `q` dismisses → `m.activeOverlay = nil`
5. Some actions (e.g., stage hunk) send messages that trigger overlay refresh

### Overlay Types

| Overlay | Trigger | Content |
|---------|---------|---------|
| DiffView | `d` | File list + unified diff |
| SessionPicker | `<leader>+s` | Fuzzy list of sessions |
| ProjectPicker | `<leader>+p` | Fuzzy list of projects |
| CommandPalette | `<leader>+c` | Fuzzy list of commands |
| CommandOutput | command execution | Scrollable command output |
| HelpOverlay | `?` | Keybind reference |

All overlays implement the `Overlay` interface and wrap bubbles components (list for pickers, viewport for content).

---

## Diff Engine

### Parsing Strategy

**Option A: `sourcegraph/go-diff` library**
- Parses unified diff format into structured `FileDiff` and `Hunk` types
- Well-tested, handles edge cases
- Recommended for MVP

**Option B: Manual parsing**
- State machine parsing `git diff` output
- More control, fewer dependencies
- Use if go-diff proves insufficient

**Recommended: Start with `sourcegraph/go-diff`, fall back to manual parsing only if needed.**

### Git Commands

```bash
# Unstaged changes (working tree vs index)
git diff --no-color --unified=3

# Staged changes (index vs HEAD)
git diff --cached --no-color --unified=3

# Untracked files
git ls-files --others --exclude-standard

# File status summary
git status --porcelain

# Stage a hunk (pipe patch to stdin)
git apply --cached --unidiff-zero -

# Unstage a hunk
git apply --cached --reverse --unidiff-zero -

# Discard a hunk (from working tree)
git apply --reverse --unidiff-zero -

# Stage entire file
git add <path>

# Unstage entire file
git reset HEAD -- <path>

# Discard entire file (tracked)
git checkout -- <path>
```

### Hunk Patch Construction

To stage/unstage/discard individual hunks, we construct a minimal patch:

```go
func BuildHunkPatch(file *FileDiff, hunk *Hunk) string {
    var buf strings.Builder

    // File header
    fmt.Fprintf(&buf, "diff --git a/%s b/%s\n", file.Path, file.Path)
    fmt.Fprintf(&buf, "--- a/%s\n", file.Path)
    fmt.Fprintf(&buf, "+++ b/%s\n", file.Path)

    // Hunk header
    fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@",
        hunk.OldStart, hunk.OldCount,
        hunk.NewStart, hunk.NewCount)
    if hunk.Header != "" {
        fmt.Fprintf(&buf, " %s", hunk.Header)
    }
    buf.WriteRune('\n')

    // Hunk lines
    for _, line := range hunk.Lines {
        switch line.Type {
        case LineContext:
            buf.WriteRune(' ')
        case LineAdd:
            buf.WriteRune('+')
        case LineDelete:
            buf.WriteRune('-')
        }
        buf.WriteString(line.Content)
        buf.WriteRune('\n')
    }

    return buf.String()
}
```

### Refresh Flow

After any hunk action:
1. Execute the git command (apply patch, add, checkout, etc.)
2. Re-fetch both `git diff` and `git diff --cached`
3. Re-parse into `[]FileDiff`
4. Update the diff model, preserving cursor position where possible
5. If the current file has no more hunks, move to the next file

---

## Command System

### YAML Loading

```go
func (l *Loader) LoadCommands() ([]*Command, error) {
    var commands []*Command

    // Load project commands first (higher priority)
    projectDir := filepath.Join(l.projectDir, ".deck", "commands")
    if entries, err := os.ReadDir(projectDir); err == nil {
        for _, e := range entries {
            if filepath.Ext(e.Name()) == ".yaml" || filepath.Ext(e.Name()) == ".yml" {
                cmd, err := l.loadFile(filepath.Join(projectDir, e.Name()))
                if err != nil {
                    continue // skip invalid files
                }
                commands = append(commands, cmd)
            }
        }
    }

    // Load global commands (lower priority, skip if name conflicts)
    globalDir := filepath.Join(l.globalDir, "commands")
    // ... same pattern, dedup by name ...

    return commands, nil
}
```

### Context Variable Resolution with Lazy Evaluation

```go
func NewContext(projectPath, sessionName string) *Context {
    return &Context{
        ProjectPath:   projectPath,
        ProjectName:   filepath.Base(projectPath),
        SessionName:   sessionName,
        CurrentBranch: lazyGit(projectPath, "git", "rev-parse", "--abbrev-ref", "HEAD"),
        DefaultBranch: lazyGit(projectPath, "git", "config", "init.defaultBranch"),
        StagedDiff:    lazyGit(projectPath, "git", "diff", "--cached", "--no-color"),
        UnstagedDiff:  lazyGit(projectPath, "git", "diff", "--no-color"),
        ChangedFiles:  lazyGit(projectPath, "git", "diff", "--name-only"),
        StagedFiles:   lazyGit(projectPath, "git", "diff", "--cached", "--name-only"),
    }
}

// lazyGit returns a function that executes a git command on first call.
func lazyGit(dir string, args ...string) func() string {
    var once sync.Once
    var result string
    return func() string {
        once.Do(func() {
            cmd := exec.Command(args[0], args[1:]...)
            cmd.Dir = dir
            out, err := cmd.Output()
            if err != nil {
                result = ""
                return
            }
            result = strings.TrimSpace(string(out))
        })
        return result
    }
}
```

### Template Rendering

```go
func RenderTemplate(cmdTemplate string, ctx *Context) (string, error) {
    funcMap := template.FuncMap{
        "project_path":   func() string { return ctx.ProjectPath },
        "project_name":   func() string { return ctx.ProjectName },
        "current_branch": func() string { return ctx.CurrentBranch() },
        "default_branch": func() string { return ctx.DefaultBranch() },
        "staged_diff":    ctx.StagedDiff,    // lazy, called only if referenced
        "unstaged_diff":  ctx.UnstagedDiff,
        "changed_files":  ctx.ChangedFiles,
        "staged_files":   ctx.StagedFiles,
        "session_name":   func() string { return ctx.SessionName },
    }

    tmpl, err := template.New("cmd").Funcs(funcMap).Parse(cmdTemplate)
    if err != nil {
        return "", fmt.Errorf("template parse: %w", err)
    }

    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, nil); err != nil {
        return "", fmt.Errorf("template execute: %w", err)
    }

    return buf.String(), nil
}
```

**Note:** Using `{{function_name}}` as template syntax naturally maps to Go's `text/template` function calls. The context variables become template functions rather than data fields, enabling lazy evaluation.

### Command Execution

```go
func ExecuteCommand(cmd *Command, rendered string, workDir string) tea.Cmd {
    switch cmd.Output {
    case "session":
        // Create a new session with the command
        return func() tea.Msg {
            return CreateSessionMsg{
                Name:    cmd.Name,
                Command: rendered,
                WorkDir: workDir,
            }
        }
    case "overlay", "fullscreen":
        // Capture output
        return func() tea.Msg {
            out, err := exec.Command("sh", "-c", rendered).CombinedOutput()
            if err != nil {
                return CommandOutputMsg{
                    Content: fmt.Sprintf("Error: %s\n%s", err, string(out)),
                    IsError: true,
                }
            }
            return CommandOutputMsg{Content: string(out)}
        }
    }
    return nil
}
```

---

## State Management

### Elm Architecture Mapping

Bubble Tea enforces the Elm architecture: unidirectional data flow with immutable model updates.

```
State Tree:
app.Model
├── mode: Mode
├── keymap: Keymap
├── projectMgr: *project.Manager
│   ├── projects: []*Project
│   │   ├── [0]: Project
│   │   │   ├── sessionMgr: *session.Manager
│   │   │   │   ├── sessions: []*Session
│   │   │   │   │   ├── [0]: Session (pty, vt, scrollOff, ...)
│   │   │   │   │   └── [1]: Session
│   │   │   │   └── active: 0
│   │   │   ├── config: *ProjectConfig
│   │   │   └── commands: []*Command
│   │   └── [1]: Project
│   └── active: 0
├── activeOverlay: Overlay (nil or one of the overlay types)
├── statusBar: statusbar.Model
├── leaderBuffer: []rune
├── leaderTimer: *time.Timer
└── width, height: int
```

### Message Flow

Messages in Bubble Tea flow through a single `Update()` call. The root model dispatches:

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmds []tea.Cmd

    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.width = msg.Width
        m.height = msg.Height
        // Propagate to all sub-models...

    case tea.KeyMsg:
        if m.mode == ModeInsert {
            // Forward to active session PTY
            cmd := m.forwardKeyToSession(msg)
            cmds = append(cmds, cmd)
        } else {
            // Normal mode: check overlay first, then keymap
            if m.activeOverlay != nil {
                cmd := m.updateOverlay(msg)
                cmds = append(cmds, cmd)
            } else {
                cmd := m.handleNormalKey(msg)
                cmds = append(cmds, cmd)
            }
        }

    case TerminalOutputMsg:
        // Session has new output — View() will re-render
        // Optionally update status bar with git info

    case SessionExitedMsg:
        cmd := m.handleSessionExit(msg)
        cmds = append(cmds, cmd)

    // ... other message types
    }

    return m, tea.Batch(cmds...)
}
```

### Leader Key Sequences

Leader key sequences require buffering:

```go
func (m *Model) handleNormalKey(msg tea.KeyMsg) tea.Cmd {
    keyStr := msg.String()

    // If we're in a leader sequence
    if m.leaderActive {
        m.leaderActive = false
        m.leaderTimer.Stop()

        switch keyStr {
        case "s":
            return m.showSessionPicker()
        case "p":
            return m.showProjectPicker()
        case "n":
            return m.createNewSession()
        case "c":
            return m.showCommandPalette()
        case "o":
            return m.openProject()
        }
        return nil
    }

    // Check for leader key press
    if keyStr == m.keymap.Leader {
        m.leaderActive = true
        m.leaderTimer = time.AfterFunc(300*time.Millisecond, func() {
            m.leaderActive = false
        })
        return nil
    }

    // Direct keybinds
    switch {
    case key.Matches(msg, m.keymap.ToggleDiff):
        return m.toggleDiff()
    case key.Matches(msg, m.keymap.EnterInsert):
        m.mode = ModeInsert
        return nil
    case key.Matches(msg, m.keymap.ScrollUp):
        return m.scrollUp()
    case key.Matches(msg, m.keymap.ScrollDown):
        return m.scrollDown()
    case key.Matches(msg, m.keymap.DismissOverlay):
        m.activeOverlay = nil
        return nil
    }

    return nil
}
```

**Note on leader timer:** The `time.AfterFunc` approach has a race condition with the Bubble Tea event loop. A safer approach is to send a `LeaderTimeoutMsg` via `tea.Tick` and handle it in Update():

```go
func leaderTimeout() tea.Cmd {
    return tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
        return LeaderTimeoutMsg{}
    })
}
```

---

## Configuration Loading

### Load Order

```
1. hardcodedDefaults()           → Config with all fields set
2. loadYAML(~/.config/deck/config.yaml)  → override non-zero fields
3. loadYAML(.deck/config.yaml)   → override non-zero fields
```

### Implementation

```go
func Load(projectPath string) (*Config, error) {
    cfg := hardcodedDefaults()

    // Global config
    globalPath := filepath.Join(configDir(), "config.yaml")
    if data, err := os.ReadFile(globalPath); err == nil {
        if err := yaml.Unmarshal(data, cfg); err != nil {
            return nil, fmt.Errorf("global config: %w", err)
        }
    }

    // Project config
    projectCfgPath := filepath.Join(projectPath, ".deck", "config.yaml")
    if data, err := os.ReadFile(projectCfgPath); err == nil {
        if err := yaml.Unmarshal(data, cfg); err != nil {
            return nil, fmt.Errorf("project config: %w", err)
        }
    }

    return cfg, nil
}

func configDir() string {
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return filepath.Join(xdg, "deck")
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "deck")
}
```

### Hot Reload

Not implemented in MVP. Files are read once at startup and when a new project is added. If users edit config while Deck is running, they restart.

---

## Session Persistence

### Schema (`.deck/sessions.yaml`)

```yaml
active: "auth-feature"
sessions:
  - name: "auth-feature"
    command: "claude"
    workdir: "/home/user/myapp"
  - name: "bugfix"
    command: "claude"
    workdir: "/home/user/myapp"
  - name: "shell"
    command: "bash"
    workdir: "/home/user/myapp"
```

### What's Persisted

| Persisted | Not Persisted |
|-----------|--------------|
| Session name | Terminal content/scrollback |
| Command | PTY state |
| Working directory | Cursor position |
| Active session indicator | Environment mutations |
| Session order | Running process state |

### Restore Behavior

On `deck /path/to/project`:
1. Check for `.deck/sessions.yaml`
2. If exists: recreate each session with its command and workdir, activate the previously active session
3. If not exists: create a single default session named "main" with the configured default shell
4. Session processes start fresh — terminal history depends on the agent's own persistence

---

## Distribution

### goreleaser Configuration

```yaml
# .goreleaser.yaml
project_name: deck
builds:
  - main: ./cmd/deck
    binary: deck
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    env:
      - CGO_ENABLED=0
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - format: tar.gz
    name_template: "deck_{{ .Os }}_{{ .Arch }}"

brews:
  - repository:
      owner: syndg
      name: homebrew-tap
    homepage: "https://github.com/syndg/deck"
    description: "Terminal-native environment for agentic coding workflows"
    install: |
      bin.install "deck"

checksum:
  name_template: "checksums.txt"

changelog:
  sort: asc
```

### npm Platform-Specific Binary Pattern

Following the esbuild distribution model:

```
@syndg/deck                    ← main package (detects platform, requires correct binary pkg)
@syndg/deck-darwin-arm64       ← macOS ARM binary
@syndg/deck-darwin-x64         ← macOS Intel binary
@syndg/deck-linux-arm64        ← Linux ARM binary
@syndg/deck-linux-x64          ← Linux x86_64 binary
```

Main package `postinstall` script:
```javascript
const { platform, arch } = process;
const mapping = {
    "darwin-arm64": "@syndg/deck-darwin-arm64",
    "darwin-x64":   "@syndg/deck-darwin-x64",
    "linux-arm64":  "@syndg/deck-linux-arm64",
    "linux-x64":    "@syndg/deck-linux-x64",
};
const pkg = mapping[`${platform}-${arch}`];
if (!pkg) {
    console.error(`Unsupported platform: ${platform}-${arch}`);
    process.exit(1);
}
// Binary is resolved from the optional dependency
```

The main `package.json` lists platform packages as `optionalDependencies`. npm automatically installs only the one matching the current platform.

### Installation Matrix

| Method | Command | Notes |
|--------|---------|-------|
| npm | `npm i -g @syndg/deck` | Platform-specific binary via optionalDependencies |
| bun | `bun i -g @syndg/deck` | Same as npm |
| Homebrew | `brew install syndg/tap/deck` | Via goreleaser tap |
| Go | `go install github.com/syndg/deck/cmd/deck@latest` | Requires Go toolchain |
| Binary | Download from GitHub Releases | Direct tar.gz per platform |

---

## Appendix: Key Design Decisions

### Why x/vt over raw ANSI parsing?
Full terminal emulation handles all edge cases: alternate screen, scrollback, cursor save/restore, SGR, etc. Raw parsing would miss subtle behaviors and break with complex TUI programs running inside Deck.

### Why creack/pty over x/xpty?
`creack/pty` is the battle-tested standard for PTY creation in Go (4K+ stars, widely used). `x/xpty` is experimental and primarily designed for testing. For production PTY management, `creack/pty` provides the reliability and platform coverage needed.

### Why string-based overlay compositing over cell-level?
Cell-level compositing requires maintaining a parallel cell buffer for the entire screen — possible but complex. Lipgloss's `Place` and `JoinVertical` provide centered floating panels with minimal code. The visual result is equivalent for floating overlays. Cell-level compositing can be added later if needed (e.g., for transparent overlays showing terminal content underneath).

### Why `text/template` over simple string replacement?
`text/template` provides conditional logic, pipes, and functions for free. Context variables map naturally to template functions, enabling lazy evaluation. Simple `strings.Replace` would require evaluating all variables upfront even if unused.

### Why not SQLite for session persistence?
SQLite adds CGO dependency complexity and is overkill for persisting a small list of session names and commands. YAML is human-readable, trivially editable, and requires no external dependencies.
