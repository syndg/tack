# Deck — Implementation Tasks

**Usage:** Feed this file to Claude Code in a Ralph loop.
Claude picks the next unchecked task, implements it, verifies, checks it off, commits.

**Prerequisite:** Read [DESIGN.md](./DESIGN.md) for full technical context.

---

## Progress

- **Total:** 78 tasks (T-001 to T-078)
- **Completed:** 0
- **Current:** T-001

**Note:** Some tasks have multiple verification steps counted separately in the grep output.

---

## Phase 1: Foundation (T-001 to T-007)

- [ ] **T-001: Initialize Go module and project scaffold**
  - Create `go.mod` with module `github.com/syndg/deck`
  - Create directory structure:
    - `cmd/deck/`
    - `internal/app/`, `internal/session/`, `internal/project/`, `internal/diff/`, `internal/commands/`, `internal/overlay/`, `internal/statusbar/`, `internal/config/`
    - `pkg/terminal/`
    - `configs/defaults/`
  - Create `cmd/deck/main.go` with minimal `func main()` that prints "deck v0.1.0-dev"
  - **Files:** `go.mod`, `cmd/deck/main.go`, all directories (with `.gitkeep` or `doc.go`)
  - **Verify:** `go build ./cmd/deck && ./deck` prints version string
  - **Commit:** `feat: initialize project scaffold and go module`

- [ ] **T-002: Add core dependencies**
  - Run `go get`:
    - `github.com/charmbracelet/bubbletea`
    - `github.com/charmbracelet/lipgloss`
    - `github.com/charmbracelet/bubbles`
    - `github.com/charmbracelet/x/vt` (virtual terminal emulator)
    - `github.com/charmbracelet/x/ansi`
    - `github.com/charmbracelet/ultraviolet` (cell types, used by x/vt)
    - `github.com/creack/pty`
    - `gopkg.in/yaml.v3`
  - Create placeholder `doc.go` files in `pkg/terminal/` and `internal/app/`
  - **Files:** `go.mod`, `go.sum`, `pkg/terminal/doc.go`, `internal/app/doc.go`
  - **Verify:** `go build ./...` succeeds
  - **Commit:** `chore: add core dependencies`

- [ ] **T-003: Implement config types**
  - Create `internal/config/config.go` with:
    - `Config` struct (DefaultShell, Leader, Scrollback, Theme, Keybinds)
    - `ThemeConfig` struct (DiffAdd, DiffDelete, DiffContext, StatusBar, OverlayBorder)
    - `KeybindConfig` struct (ToggleDiff, SessionPicker, ProjectPicker, NewSession, CommandPalette)
    - `Defaults()` function returning hardcoded defaults per DESIGN.md
  - **Files:** `internal/config/config.go`
  - **Verify:** `go build ./internal/config/...`
  - **Commit:** `feat(config): define config types with defaults`

- [ ] **T-004: Implement config YAML loading**
  - Add to `internal/config/loader.go`:
    - `configDir()` helper (XDG_CONFIG_HOME or ~/.config/deck)
    - `Load(projectPath string) (*Config, error)` function
    - Merge strategy: defaults → global config → project config
  - **Files:** `internal/config/loader.go`
  - **Verify:** `go build ./internal/config/...`
  - **Commit:** `feat(config): implement YAML config loading with merge strategy`

- [ ] **T-005: Add config loading tests**
  - Create `internal/config/config_test.go` with tests:
    - `TestDefaults` — verify default values
    - `TestLoadGlobalConfig` — mock global config file, verify override
    - `TestLoadProjectConfig` — mock project config, verify override
    - `TestMergeStrategy` — verify field-level merge
  - Use `t.TempDir()` for test fixtures
  - **Files:** `internal/config/config_test.go`
  - **Verify:** `go test ./internal/config/... -v` — all pass
  - **Commit:** `test(config): add config loading tests`

- [ ] **T-006: Create default config file template**
  - Create `configs/defaults/config.yaml` with all default values documented
  - Include comments explaining each option
  - **Files:** `configs/defaults/config.yaml`
  - **Verify:** File exists and is valid YAML (parse with `go run` script or manual check)
  - **Commit:** `feat(config): add default config template`

- [ ] **T-007: Implement Mode and Keymap types**
  - Create `internal/app/mode.go` with:
    - `Mode` type (ModeNormal, ModeInsert constants)
    - `String()` method for Mode
  - Create `internal/app/keymap.go` with:
    - `Keymap` struct using `github.com/charmbracelet/bubbles/key`
    - `DefaultKeymap()` function returning default bindings per DESIGN.md
    - Leader key field (default: space)
  - **Files:** `internal/app/mode.go`, `internal/app/keymap.go`
  - **Verify:** `go build ./internal/app/...`
  - **Commit:** `feat(app): implement Mode and Keymap types`

---

## Phase 2: Bubble Tea Shell (T-008 to T-014)

- [ ] **T-008: Create minimal root model**
  - Create `internal/app/model.go` with:
    - `Model` struct (mode, keymap, width, height, quitting fields)
    - `New() Model` constructor
    - Stub `Init() tea.Cmd` returning nil
    - Stub `Update(tea.Msg) (tea.Model, tea.Cmd)` handling only `tea.KeyMsg` for quit (ctrl+c)
    - Stub `View() string` returning "deck v0.1.0-dev"
  - **Files:** `internal/app/model.go`
  - **Verify:** `go build ./internal/app/...`
  - **Commit:** `feat(app): create minimal root Bubble Tea model`

- [ ] **T-009: Wire up tea.Program in main**
  - Update `cmd/deck/main.go` to:
    - Create `app.New()` model
    - Create `tea.NewProgram(model, tea.WithAltScreen())`
    - Run program and handle error
    - Clean exit on error
  - **Files:** `cmd/deck/main.go`
  - **Verify:** `go build ./cmd/deck && ./deck` shows "deck v0.1.0-dev", Ctrl+C exits cleanly
  - **Commit:** `feat: wire up Bubble Tea program in main`

- [ ] **T-010: Handle WindowSizeMsg**
  - Update `internal/app/model.go` Update() to handle `tea.WindowSizeMsg`:
    - Store width and height
  - Update View() to show dimensions: "deck | 80x24" (using stored values)
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, resize terminal, verify dimensions update
  - **Commit:** `feat(app): handle terminal resize events`

- [ ] **T-011: Implement mode switching**
  - Update `internal/app/model.go`:
    - Add mode switching: Esc → Normal, 'i' → Insert (when in Normal)
    - Update View() to show current mode: "[NORMAL] deck | 80x24"
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, press 'i' to enter insert, Esc to return to normal, mode indicator updates
  - **Commit:** `feat(app): implement vim-style mode switching`

- [ ] **T-012: Implement leader key buffer**
  - Create `internal/app/leader.go` with:
    - `LeaderState` struct (active bool, timer time.Time)
    - `LeaderTimeoutMsg` type
    - Logic to handle leader key press and timeout
  - Update `internal/app/model.go`:
    - Add leaderState to Model
    - Handle leader key (space) in normal mode → set active
    - Handle LeaderTimeoutMsg → clear active
    - Use `tea.Tick` for timeout (300ms)
  - **Files:** `internal/app/leader.go`, `internal/app/model.go`
  - **Verify:** Run `./deck`, press space, wait, verify timeout clears state (add debug output temporarily)
  - **Commit:** `feat(app): implement leader key buffer with timeout`

- [ ] **T-013: Add graceful shutdown**
  - Update `internal/app/model.go`:
    - Add `Quit()` method that sets quitting=true and returns `tea.Quit`
    - Handle 'q' in normal mode (no overlay) to quit
    - Clean up any resources in cleanup
  - Update `cmd/deck/main.go`:
    - Handle program.Run() error
    - Print any final error message
  - **Files:** `internal/app/model.go`, `cmd/deck/main.go`
  - **Verify:** Run `./deck`, press 'q' to quit, verify clean exit
  - **Commit:** `feat(app): add graceful shutdown handling`

- [ ] **T-014: Add basic logging setup**
  - Create `internal/app/log.go` with:
    - Debug logging to file (when DECK_DEBUG=1)
    - Log file at `~/.config/deck/deck.log`
    - `LogDebug(format, args...)` function
  - Wire into Init() to log startup
  - **Files:** `internal/app/log.go`, update `internal/app/model.go`
  - **Verify:** Set DECK_DEBUG=1, run `./deck`, check log file exists with startup message
  - **Commit:** `feat(app): add debug logging infrastructure`

---

## Phase 3: PTY Integration (T-015 to T-023)

- [ ] **T-015: Implement PTY wrapper**
  - Create `pkg/terminal/pty.go` with:
    - `PTY` struct (file *os.File, cmd *exec.Cmd, done chan struct{})
    - `NewPTY(command string, args []string, workDir string, rows, cols int) (*PTY, error)`
    - `Write(data []byte) (int, error)` — write to PTY stdin
    - `Read(buf []byte) (int, error)` — read from PTY stdout
    - `Close() error` — close PTY and kill process
    - `Done() <-chan struct{}` — returns done channel
  - Use `creack/pty.StartWithSize` for creation
  - **Files:** `pkg/terminal/pty.go`
  - **Verify:** `go build ./pkg/terminal/...`
  - **Commit:** `feat(terminal): implement PTY wrapper`

- [ ] **T-016: Implement PTY resize**
  - Add to `pkg/terminal/pty.go`:
    - `Resize(rows, cols int) error` using `pty.Setsize`
  - **Files:** `pkg/terminal/pty.go`
  - **Verify:** `go build ./pkg/terminal/...`
  - **Commit:** `feat(terminal): add PTY resize support`

- [ ] **T-017: Add PTY tests**
  - Create `pkg/terminal/pty_test.go` with:
    - `TestNewPTY` — create PTY with /bin/echo, verify output
    - `TestPTYWrite` — write to PTY, verify child receives
    - `TestPTYResize` — create and resize, verify no error
    - `TestPTYClose` — create and close, verify done channel closes
  - **Files:** `pkg/terminal/pty_test.go`
  - **Verify:** `go test ./pkg/terminal/... -v` — all pass
  - **Commit:** `test(terminal): add PTY wrapper tests`

- [ ] **T-018: Implement VT wrapper structure with x/vt SafeEmulator**
  - Create `pkg/terminal/vt.go` with:
    - `VT` struct wrapping `*vt.SafeEmulator` + history RingBuffer
    - `RingBuffer` struct for scrollback history (fixed-size circular buffer)
    - `NewVT(width, height, scrollback int) *VT` — creates SafeEmulator
    - `Write(data []byte) (int, error)` — delegates to emu.Write()
    - `Resize(width, height int)` — calls emu.Resize()
    - `Render(scrollOffset int) string` — returns emu.Render() when offset=0
    - `Width()`, `Height()`, `TotalLines()` methods
  - Use `github.com/charmbracelet/x/vt` package
  - **Files:** `pkg/terminal/vt.go`
  - **Verify:** `go build ./pkg/terminal/...`
  - **Commit:** `feat(terminal): implement VT wrapper with x/vt SafeEmulator`

- [ ] **T-019: Implement scrollback history capture**
  - Update `pkg/terminal/vt.go`:
    - Implement `RingBuffer` with `Push()`, `Last(n)`, `Len()` methods
    - Add callback to capture lines scrolling off screen
    - Use x/vt's callback mechanism (`SetCallbacks`) to detect scroll events
    - OR poll `Touched()` lines to detect scrolling
    - Store scrolled-off lines in RingBuffer
  - Handle alternate screen mode (pause scrollback capture when active)
  - **Files:** `pkg/terminal/vt.go`
  - **Verify:** `go build ./pkg/terminal/...`
  - **Commit:** `feat(terminal): implement scrollback history capture`

- [ ] **T-020: Implement history-aware rendering**
  - Update `pkg/terminal/vt.go`:
    - When `scrollOffset > 0`, render from history + current screen
    - Implement `renderWithHistory(offset int) string`
    - Handle edge cases: offset beyond history, partial screen
  - Add VT tests in `pkg/terminal/vt_test.go`:
    - `TestVTWrite` — write text, verify Render() returns it
    - `TestVTScrollback` — write many lines, verify history captured
    - `TestVTRenderWithOffset` — scroll up, verify correct content
    - `TestVTResize` — resize, verify dimensions update
  - **Files:** `pkg/terminal/vt.go`, `pkg/terminal/vt_test.go`
  - **Verify:** `go test ./pkg/terminal/... -v` — all pass
  - **Commit:** `feat(terminal): implement history-aware rendering`

- [ ] **T-021: Create Session struct**
  - Create `internal/session/session.go` with:
    - `Session` struct (ID, Name, Command, WorkDir, pty *terminal.PTY, vt *terminal.VT, scrollOff, width, height, exited)
    - `New(name, command, workDir string, width, height int) (*Session, error)`
    - `WriteInput(data []byte) error` — forward to PTY
    - `View() string` — return VT.Render(scrollOff)
    - `Resize(width, height int) error`
    - `Close() error`
  - **Files:** `internal/session/session.go`
  - **Verify:** `go build ./internal/session/...`
  - **Commit:** `feat(session): create Session struct with PTY/VT`

- [ ] **T-022: Implement PTY output reading goroutine**
  - Add to `internal/session/session.go`:
    - `TerminalOutputMsg` struct with SessionID
    - `SessionExitedMsg` struct with SessionID, ExitCode
    - `Start(program *tea.Program) error` method that spawns read goroutine
    - Goroutine reads PTY, writes to VT, sends TerminalOutputMsg
  - **Files:** `internal/session/session.go`
  - **Verify:** `go build ./internal/session/...`
  - **Commit:** `feat(session): implement PTY output reading goroutine`

- [ ] **T-023: Integrate session into app model**
  - Update `internal/app/model.go`:
    - Add session *session.Session field
    - In Init(), create a session with default shell ($SHELL or /bin/sh)
    - Handle TerminalOutputMsg — just trigger re-render
    - Handle SessionExitedMsg — for now, quit app
    - Update View() to show session.View() instead of static text
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, verify shell appears and output displays
  - **Commit:** `feat(app): integrate session into root model`

---

## Phase 4: Terminal Interaction (T-024 to T-030)

- [ ] **T-024: Implement key-to-bytes conversion**
  - Create `internal/app/keys.go` with:
    - `keyToBytes(msg tea.KeyMsg) []byte` function
    - Handle regular characters (UTF-8 encode)
    - Handle control keys (Ctrl+C → 0x03, etc.)
    - Handle special keys (arrows → ANSI sequences, Enter → 0x0D, etc.)
  - **Files:** `internal/app/keys.go`
  - **Verify:** `go build ./internal/app/...`
  - **Commit:** `feat(app): implement key-to-bytes conversion for PTY input`

- [ ] **T-025: Forward keys to PTY in insert mode**
  - Update `internal/app/model.go`:
    - In Update(), when mode == ModeInsert and key is not Esc
    - Convert key to bytes using keyToBytes()
    - Call session.WriteInput(bytes)
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, press 'i', type commands, verify shell receives input
  - **Commit:** `feat(app): forward keystrokes to PTY in insert mode`

- [ ] **T-026: Implement terminal scroll in VT**
  - Update `pkg/terminal/vt.go`:
    - `TotalLines() int` method
    - Update `Render(scrollOffset int)` to respect scroll offset
    - Return visible window based on (totalLines - height - scrollOffset)
  - **Files:** `pkg/terminal/vt.go`
  - **Verify:** `go build ./pkg/terminal/...`
  - **Commit:** `feat(terminal): implement scroll offset in VT render`

- [ ] **T-027: Implement scroll commands in normal mode**
  - Update `internal/app/model.go`:
    - Handle Ctrl+U in normal mode → decrease scrollOff (scroll up)
    - Handle Ctrl+D in normal mode → increase scrollOff (scroll down)
    - Clamp scrollOff to valid range [0, totalLines-height]
  - Update session.go:
    - Add `ScrollUp(lines int)` and `ScrollDown(lines int)` methods
    - Add getter for scroll position
  - **Files:** `internal/app/model.go`, `internal/session/session.go`
  - **Verify:** Run `./deck`, run command with lots of output, press Esc, use Ctrl+U/D to scroll
  - **Commit:** `feat(app): implement terminal scrolling in normal mode`

- [ ] **T-028: Handle resize propagation**
  - Update `internal/app/model.go`:
    - In WindowSizeMsg handler, calculate content height (height - 1 for status bar)
    - Call session.Resize(width, contentHeight)
  - Update `internal/session/session.go`:
    - In Resize(), call both pty.Resize() and vt.Resize()
  - **Files:** `internal/app/model.go`, `internal/session/session.go`
  - **Verify:** Run `./deck`, resize terminal, verify shell adapts (run `stty size` in shell)
  - **Commit:** `feat(app): propagate resize to PTY and VT`

- [ ] **T-029: Handle session exit gracefully**
  - Update `internal/app/model.go`:
    - On SessionExitedMsg, show "Session exited (code: X)" message
    - Allow user to quit with 'q' or create new session
  - Update View() to show exit message when session is nil/exited
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, type `exit` in shell, verify exit message shows
  - **Commit:** `feat(app): handle session exit gracefully`

- [ ] **T-030: Add session cleanup on app quit**
  - Update `internal/app/model.go`:
    - In Quit(), close all sessions
  - Create `internal/app/cleanup.go` with cleanup logic
  - **Files:** `internal/app/model.go`, `internal/app/cleanup.go`
  - **Verify:** Run `./deck`, quit with 'q', verify no zombie processes (`ps aux | grep deck`)
  - **Commit:** `feat(app): clean up sessions on quit`

---

## Phase 5: Session Management (T-031 to T-037)

- [ ] **T-031: Create Session Manager**
  - Create `internal/session/manager.go` with:
    - `Manager` struct (sessions []*Session, active int, projectID string)
    - `NewManager(projectID string) *Manager`
    - `Add(session *Session)` — add session to list
    - `Active() *Session` — return active session
    - `SetActive(index int) error` — switch active session
    - `Remove(id string) error` — close and remove session
    - `List() []*Session` — return all sessions
  - **Files:** `internal/session/manager.go`
  - **Verify:** `go build ./internal/session/...`
  - **Commit:** `feat(session): create Session Manager`

- [ ] **T-032: Integrate Manager into app**
  - Update `internal/app/model.go`:
    - Replace single session with *session.Manager
    - Create manager in Init()
    - Add default session to manager
    - Route terminal messages to correct session via ID
    - Update View() to use manager.Active().View()
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, verify single session still works
  - **Commit:** `feat(app): integrate session manager`

- [ ] **T-033: Implement session creation**
  - Add to `internal/session/manager.go`:
    - `Create(name, command, workDir string) (*Session, error)`
    - Auto-generate session ID
  - Add to `internal/app/model.go`:
    - Handle `<leader>+n` to create new session
    - Create session with default shell
    - Switch to new session
  - Add `CreateSessionMsg` type
  - **Files:** `internal/session/manager.go`, `internal/app/model.go`
  - **Verify:** Run `./deck`, press `<space>n`, verify new session created
  - **Commit:** `feat(session): implement session creation`

- [ ] **T-034: Implement session switching**
  - Add to `internal/app/model.go`:
    - Handle number keys 1-9 in normal mode to switch sessions
    - Add session count to display
  - Update View() to show session indicator: "[session 1/2]"
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, create 2nd session, press 1/2 to switch between them
  - **Commit:** `feat(app): implement session switching with number keys`

- [ ] **T-035: Define session persistence types**
  - Create `internal/session/persist.go` with:
    - `SessionLayout` struct (Name, Command, WorkDir)
    - `LayoutFile` struct (Active string, Sessions []SessionLayout)
  - **Files:** `internal/session/persist.go`
  - **Verify:** `go build ./internal/session/...`
  - **Commit:** `feat(session): define persistence types`

- [ ] **T-036: Implement session save**
  - Add to `internal/session/persist.go`:
    - `SaveLayout(path string, manager *Manager) error`
    - Serialize manager state to YAML
    - Write to `.deck/sessions.yaml`
  - **Files:** `internal/session/persist.go`
  - **Verify:** `go build ./internal/session/...`
  - **Commit:** `feat(session): implement session layout save`

- [ ] **T-037: Implement session restore**
  - Add to `internal/session/persist.go`:
    - `LoadLayout(path string) (*LayoutFile, error)`
  - Update `internal/session/manager.go`:
    - `RestoreFromLayout(layout *LayoutFile, program *tea.Program) error`
    - Recreate sessions from layout
    - Set active session
  - **Files:** `internal/session/persist.go`, `internal/session/manager.go`
  - **Verify:** Create sessions, quit, restart, verify sessions restored
  - **Commit:** `feat(session): implement session layout restore`

---

## Phase 6: Status Bar (T-038 to T-042)

- [ ] **T-038: Create status bar model**
  - Create `internal/statusbar/statusbar.go` with:
    - `Model` struct (mode, project, session, branch, width)
    - `New() Model`
    - `Update(msg tea.Msg) (Model, tea.Cmd)` — handle update messages
    - `View() string` — render status bar
  - Render format: `[MODE] deck | project: X | session: Y`
  - **Files:** `internal/statusbar/statusbar.go`
  - **Verify:** `go build ./internal/statusbar/...`
  - **Commit:** `feat(statusbar): create status bar model`

- [ ] **T-039: Add change stats to status bar**
  - Update `internal/statusbar/statusbar.go`:
    - Add `ChangeStats` struct (Modified, Added, Deleted, Untracked int)
    - Add `staged int` field
    - Update View() to show: `| 3M 1A | 4 staged`
  - Define `GitStatsMsg` for updates
  - **Files:** `internal/statusbar/statusbar.go`
  - **Verify:** `go build ./internal/statusbar/...`
  - **Commit:** `feat(statusbar): add git change stats display`

- [ ] **T-040: Integrate status bar into app**
  - Update `internal/app/model.go`:
    - Add statusBar statusbar.Model field
    - Initialize in New()
    - Update View() to render: content + "\n" + statusBar.View()
    - Forward ModeChangedMsg, WindowSizeMsg to status bar
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, verify status bar appears at bottom
  - **Commit:** `feat(app): integrate status bar`

- [ ] **T-041: Implement git stats fetching**
  - Create `internal/statusbar/git.go` with:
    - `FetchGitStats(workDir string) tea.Cmd` — async command
    - Parse `git status --porcelain` output
    - Parse `git diff --cached --stat` for staged count
    - Return GitStatsMsg
  - **Files:** `internal/statusbar/git.go`
  - **Verify:** `go build ./internal/statusbar/...`
  - **Commit:** `feat(statusbar): implement git stats fetching`

- [ ] **T-042: Wire git stats refresh**
  - Update `internal/app/model.go`:
    - Fetch git stats on startup
    - Refresh on mode change to normal
    - Handle GitStatsMsg, forward to status bar
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck` in git repo, verify change counts show in status bar
  - **Commit:** `feat(app): wire git stats refresh`

---

## Phase 7: Overlay System (T-043 to T-048)

- [ ] **T-043: Define Overlay interface**
  - Create `internal/overlay/overlay.go` with:
    - `Overlay` interface (tea.Model + ID(), Title(), Fullscreen(), SetFullscreen(), SetSize())
    - `OverlayDismissMsg` type
    - `OverlayFullscreenToggleMsg` type
  - **Files:** `internal/overlay/overlay.go`
  - **Verify:** `go build ./internal/overlay/...`
  - **Commit:** `feat(overlay): define Overlay interface`

- [ ] **T-044: Implement overlay rendering in app View**
  - Update `internal/app/model.go`:
    - Add activeOverlay Overlay field
    - Update View() to use lipgloss.Place for centered overlay
    - Handle fullscreen overlay mode
    - Calculate overlay dimensions (80% of content area)
  - **Files:** `internal/app/model.go`
  - **Verify:** `go build ./internal/app/...`
  - **Commit:** `feat(app): implement overlay rendering compositor`

- [ ] **T-045: Implement overlay key handling**
  - Update `internal/app/model.go`:
    - When overlay active, delegate keys to overlay first
    - Handle 'q' → OverlayDismissMsg → clear activeOverlay
    - Handle 'f' → toggle overlay fullscreen
    - Handle OverlayDismissMsg from overlay sub-models
  - **Files:** `internal/app/model.go`
  - **Verify:** `go build ./internal/app/...`
  - **Commit:** `feat(app): implement overlay key handling`

- [ ] **T-046: Create simple text overlay**
  - Create `internal/overlay/text.go` with:
    - `TextOverlay` struct implementing Overlay interface
    - `NewTextOverlay(title, content string) *TextOverlay`
    - View() renders content in a bordered box with title
    - Handles j/k for scrolling content (using viewport)
  - **Files:** `internal/overlay/text.go`
  - **Verify:** `go build ./internal/overlay/...`
  - **Commit:** `feat(overlay): create simple text overlay`

- [ ] **T-047: Wire help overlay**
  - Update `internal/app/model.go`:
    - Handle '?' in normal mode → show help overlay
    - Help content: list of keybindings from keymap
  - Create `internal/app/help.go` with help text generation
  - **Files:** `internal/app/model.go`, `internal/app/help.go`
  - **Verify:** Run `./deck`, press '?', verify help overlay appears with keybinds
  - **Commit:** `feat(app): add help overlay`

- [ ] **T-048: Create list overlay base**
  - Create `internal/overlay/list.go` with:
    - `ListOverlay` struct wrapping bubbles/list
    - Implements Overlay interface
    - Handles j/k navigation, Enter selection, / for filter
    - Emits `ItemSelectedMsg{ID string}` on selection
  - **Files:** `internal/overlay/list.go`
  - **Verify:** `go build ./internal/overlay/...`
  - **Commit:** `feat(overlay): create list overlay base component`

---

## Phase 8: Session Picker (T-049 to T-052)

- [ ] **T-049: Create session picker overlay**
  - Create `internal/overlay/sessions.go` with:
    - `SessionPickerOverlay` using ListOverlay
    - `NewSessionPicker(sessions []*session.Session) *SessionPickerOverlay`
    - Items show session name and command
    - Emits `SessionSelectedMsg{SessionID string}` on selection
  - **Files:** `internal/overlay/sessions.go`
  - **Verify:** `go build ./internal/overlay/...`
  - **Commit:** `feat(overlay): create session picker overlay`

- [ ] **T-050: Wire session picker to leader key**
  - Update `internal/app/model.go`:
    - Handle `<leader>+s` → show SessionPickerOverlay
    - Populate with sessions from manager
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, create sessions, press `<space>s`, verify picker shows
  - **Commit:** `feat(app): wire session picker to leader+s`

- [ ] **T-051: Handle session selection**
  - Update `internal/app/model.go`:
    - Handle SessionSelectedMsg
    - Call manager.SetActive() with selected session
    - Dismiss overlay
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck`, create sessions, use picker to switch, verify switch works
  - **Commit:** `feat(app): handle session selection from picker`

- [ ] **T-052: Add new session option to picker**
  - Update `internal/overlay/sessions.go`:
    - Add "+ New Session" as first item
    - Special handling for new session selection
  - Handle in app: create session when "+ New Session" selected
  - **Files:** `internal/overlay/sessions.go`, `internal/app/model.go`
  - **Verify:** Run `./deck`, open picker, select "+ New Session", verify new session created
  - **Commit:** `feat(overlay): add new session option to picker`

---

## Phase 9: Diff Parser (T-053 to T-058)

- [ ] **T-053: Define diff data types**
  - Create `internal/diff/types.go` with:
    - `FileDiff` struct (Path, Status, IsStaged, Hunks)
    - `FileStatus` enum (Modified, Added, Deleted, Untracked, Renamed)
    - `Hunk` struct (OldStart, OldCount, NewStart, NewCount, Header, Lines)
    - `DiffLine` struct (Type, Content)
    - `LineType` enum (Context, Add, Delete)
  - **Files:** `internal/diff/types.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): define diff data types`

- [ ] **T-054: Implement unified diff parser**
  - Create `internal/diff/parse.go` with:
    - `Parse(diffOutput string) ([]FileDiff, error)`
    - Parse "diff --git" headers
    - Parse "@@ ... @@" hunk headers with regex
    - Parse +/- /space prefix lines into DiffLines
  - Reference lazygit pattern from research
  - **Files:** `internal/diff/parse.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement unified diff parser`

- [ ] **T-055: Add diff parser tests**
  - Create `internal/diff/parse_test.go` with:
    - `TestParseSimpleDiff` — single file, single hunk
    - `TestParseMultipleHunks` — single file, multiple hunks
    - `TestParseMultipleFiles` — multiple files
    - `TestParseNewFile` — added file diff
    - `TestParseDeletedFile` — deleted file diff
  - Include test fixture diffs
  - **Files:** `internal/diff/parse_test.go`
  - **Verify:** `go test ./internal/diff/... -v` — all pass
  - **Commit:** `test(diff): add diff parser tests`

- [ ] **T-056: Implement git diff commands**
  - Create `internal/diff/git.go` with:
    - `FetchUnstaged(workDir string) (string, error)` — run `git diff`
    - `FetchStaged(workDir string) (string, error)` — run `git diff --cached`
    - `FetchUntracked(workDir string) ([]string, error)` — run `git ls-files --others`
  - **Files:** `internal/diff/git.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement git diff commands`

- [ ] **T-057: Implement FetchAllDiffs**
  - Add to `internal/diff/git.go`:
    - `FetchAllDiffs(workDir string) ([]FileDiff, error)`
    - Fetch unstaged, staged, untracked
    - Parse and combine into unified file list
    - Mark staged vs unstaged appropriately
  - **Files:** `internal/diff/git.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement combined diff fetching`

- [ ] **T-058: Implement hunk patch builder**
  - Create `internal/diff/patch.go` with:
    - `BuildHunkPatch(file *FileDiff, hunk *Hunk) string`
    - Generate valid patch for `git apply` (file header + hunk header + lines)
    - Handle edge cases (new file, deleted file)
  - **Files:** `internal/diff/patch.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement hunk patch builder`

---

## Phase 10: Diff View UI (T-059 to T-064)

- [ ] **T-059: Create diff view model**
  - Create `internal/diff/model.go` with:
    - `Model` struct implementing overlay.Overlay
    - Fields: files, fileIdx, hunkIdx, focusPanel, viewport, width, height
    - `New(workDir string) *Model`
    - Stub Init/Update/View
  - **Files:** `internal/diff/model.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): create diff view model`

- [ ] **T-060: Implement diff view layout**
  - Update `internal/diff/model.go` View():
    - Two-panel layout: file list (30%) | diff content (70%)
    - File list shows status icon and path
    - Diff content shows hunk headers and lines
    - Use lipgloss for styling
  - **Files:** `internal/diff/model.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement diff view two-panel layout`

- [ ] **T-061: Implement file navigation**
  - Update `internal/diff/model.go` Update():
    - When focusPanel == FileList:
      - j/k to move fileIdx
      - Enter or Tab to focus diff panel
    - Update View() to highlight selected file
  - **Files:** `internal/diff/model.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement file list navigation`

- [ ] **T-062: Implement hunk navigation**
  - Update `internal/diff/model.go` Update():
    - When focusPanel == DiffContent:
      - j/k to scroll viewport
      - n/N to jump to next/prev hunk
      - Tab to focus file list
    - Track current hunk based on viewport position
    - Highlight current hunk in View()
  - **Files:** `internal/diff/model.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement hunk navigation`

- [ ] **T-063: Wire diff overlay to app**
  - Update `internal/app/model.go`:
    - Handle 'd' in normal mode → show diff overlay
    - Pass project workDir to diff.New()
  - **Files:** `internal/app/model.go`
  - **Verify:** Run `./deck` in git repo with changes, press 'd', verify diff view shows
  - **Commit:** `feat(app): wire diff overlay to 'd' key`

- [ ] **T-064: Style diff lines with colors**
  - Update `internal/diff/model.go`:
    - Green for additions
    - Red for deletions
    - Dim gray for context
    - Bright highlight for selected hunk
    - Use config theme colors
  - **Files:** `internal/diff/model.go`
  - **Verify:** Run `./deck`, open diff view, verify colors applied
  - **Commit:** `feat(diff): add syntax coloring for diff lines`

---

## Phase 11: Hunk Actions (T-065 to T-070)

- [ ] **T-065: Implement git apply commands**
  - Create `internal/diff/apply.go` with:
    - `StageHunk(workDir string, patch string) error` — `git apply --cached`
    - `UnstageHunk(workDir string, patch string) error` — `git apply --cached --reverse`
    - `DiscardHunk(workDir string, patch string) error` — `git apply --reverse`
  - Use `--unidiff-zero` flag for zero-context patches
  - **Files:** `internal/diff/apply.go`
  - **Verify:** `go build ./internal/diff/...`
  - **Commit:** `feat(diff): implement git apply commands`

- [ ] **T-066: Implement stage hunk action**
  - Update `internal/diff/model.go`:
    - Handle 's' key → stage current hunk
    - Build patch using BuildHunkPatch
    - Call StageHunk
    - Refresh diffs
    - Emit DiffRefreshMsg
  - **Files:** `internal/diff/model.go`
  - **Verify:** Run `./deck`, make changes, open diff, press 's', verify hunk staged
  - **Commit:** `feat(diff): implement stage hunk action`

- [ ] **T-067: Implement unstage hunk action**
  - Update `internal/diff/model.go`:
    - Handle 'u' key → unstage current hunk (only if viewing staged)
    - Call UnstageHunk
    - Refresh diffs
  - **Files:** `internal/diff/model.go`
  - **Verify:** Run `./deck`, stage changes, open diff on staged, press 'u', verify unstaged
  - **Commit:** `feat(diff): implement unstage hunk action`

- [ ] **T-068: Implement discard hunk action with confirmation**
  - Update `internal/diff/model.go`:
    - Handle 'x' key → show confirmation
    - If confirmed, call DiscardHunk
    - Refresh diffs
  - Create simple confirmation dialog component
  - **Files:** `internal/diff/model.go`, `internal/overlay/confirm.go`
  - **Verify:** Run `./deck`, make changes, open diff, press 'x', confirm, verify discarded
  - **Commit:** `feat(diff): implement discard hunk with confirmation`

- [ ] **T-069: Implement stage/discard entire file**
  - Update `internal/diff/model.go`:
    - Handle 'S' → stage entire file (`git add <path>`)
    - Handle 'X' → discard entire file with confirmation
  - Add commands to `internal/diff/apply.go`:
    - `StageFile(workDir, path string) error`
    - `DiscardFile(workDir, path string) error`
  - **Files:** `internal/diff/model.go`, `internal/diff/apply.go`
  - **Verify:** Test file-level stage and discard
  - **Commit:** `feat(diff): implement file-level stage and discard`

- [ ] **T-070: Handle cursor position after actions**
  - Update `internal/diff/model.go`:
    - After action, preserve cursor position where possible
    - If current file has no more hunks, move to next file
    - If no more files, show "No changes" message
  - **Files:** `internal/diff/model.go`
  - **Verify:** Stage multiple hunks in sequence, verify cursor behavior
  - **Commit:** `feat(diff): improve cursor handling after hunk actions`

---

## Phase 12: Command System (T-071 to T-078)

- [ ] **T-071: Define command types**
  - Create `internal/commands/types.go` with:
    - `Command` struct (Name, Description, Key, Cmd, Output, Confirm, Shell)
    - `Context` struct with lazy function fields for git data
    - Output mode constants (OutputOverlay, OutputFullscreen, OutputSession)
  - **Files:** `internal/commands/types.go`
  - **Verify:** `go build ./internal/commands/...`
  - **Commit:** `feat(commands): define command types`

- [ ] **T-072: Implement YAML loader**
  - Create `internal/commands/loader.go` with:
    - `Loader` struct (projectDir, globalDir)
    - `NewLoader(projectDir, globalDir string) *Loader`
    - `LoadCommands() ([]*Command, error)`
    - Load from `.deck/commands/*.yaml` (project) and global commands dir
    - Dedup by name (project takes priority)
  - **Files:** `internal/commands/loader.go`
  - **Verify:** `go build ./internal/commands/...`
  - **Commit:** `feat(commands): implement YAML command loader`

- [ ] **T-073: Implement context with lazy evaluation**
  - Create `internal/commands/context.go` with:
    - `NewContext(projectPath, sessionName string) *Context`
    - Implement lazy git command execution using sync.Once
    - All context variables from DESIGN.md
  - **Files:** `internal/commands/context.go`
  - **Verify:** `go build ./internal/commands/...`
  - **Commit:** `feat(commands): implement context with lazy git evaluation`

- [ ] **T-074: Implement template rendering**
  - Create `internal/commands/render.go` with:
    - `RenderTemplate(cmdTemplate string, ctx *Context) (string, error)`
    - Use text/template with function map for context variables
    - Handle template parse and execute errors
  - **Files:** `internal/commands/render.go`
  - **Verify:** `go build ./internal/commands/...`
  - **Commit:** `feat(commands): implement template rendering with context`

- [ ] **T-075: Create command palette overlay**
  - Create `internal/commands/palette.go` with:
    - `PaletteModel` using overlay.ListOverlay
    - Show command name and description
    - Fuzzy filter support
    - Emit `CommandSelectedMsg` on selection
  - **Files:** `internal/commands/palette.go`
  - **Verify:** `go build ./internal/commands/...`
  - **Commit:** `feat(commands): create command palette overlay`

- [ ] **T-076: Wire command palette**
  - Update `internal/app/model.go`:
    - Handle `<leader>+c` → show command palette
    - Load commands on app init
    - Handle CommandSelectedMsg
  - **Files:** `internal/app/model.go`
  - **Verify:** Create `.deck/commands/test.yaml`, run `./deck`, press `<space>c`, verify palette shows
  - **Commit:** `feat(app): wire command palette to leader+c`

- [ ] **T-077: Implement command execution**
  - Create `internal/commands/execute.go` with:
    - `Execute(cmd *Command, ctx *Context, workDir string) tea.Cmd`
    - Handle output modes: overlay (capture), fullscreen (capture), session (create session)
    - Render template before execution
  - Wire into app model
  - **Files:** `internal/commands/execute.go`, `internal/app/model.go`
  - **Verify:** Create command, run via palette, verify output shown
  - **Commit:** `feat(commands): implement command execution with output modes`

- [ ] **T-078: Create default commands**
  - Create `configs/defaults/commands/commit.yaml`:
    - Name: "Commit staged changes"
    - Cmd: interactive commit command template
  - Create `configs/defaults/commands/push.yaml`:
    - Name: "Push to origin"
  - Create `configs/defaults/commands/status.yaml`:
    - Name: "Git status"
    - Output: overlay
  - **Files:** `configs/defaults/commands/*.yaml`
  - **Verify:** Files exist and are valid YAML
  - **Commit:** `feat(commands): add default command templates`

---

## Completion Checklist

After all tasks complete:

- [ ] All `go test ./...` pass
- [ ] All `go build ./...` succeed with no errors
- [ ] `golangci-lint run` shows no issues (install if needed)
- [ ] README.md documents installation and basic usage
- [ ] `go mod tidy` run with no changes needed

---

## Notes for Claude

1. **Read DESIGN.md** before starting any task — it contains critical implementation details
2. **One task at a time** — don't batch multiple tasks
3. **Verify before committing** — run the verification step
4. **Check off completed tasks** — edit this file to mark `[x]`
5. **Reference local repos** — check `/Volumes/External/Coding/lazygit` for diff patterns, `/Volumes/External/Coding/opencode` for TUI patterns
6. **Use Bun** — not npm or pnpm per user preference
7. **Commit message format** — use conventional commits as shown
