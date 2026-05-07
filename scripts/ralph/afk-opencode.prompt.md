# ISSUES

Issue JSON is provided at the start of the prompt. It is either an array of open issues or a single issue object if the runner was invoked with an issue number.

You have also been passed recent `RALPH:` commits. Use them to avoid duplicating already-completed work.

# TASK SELECTION

Pick exactly one task.

Only pick issues that are:

1. Explicitly labeled `afk`
2. Not blocked by any open issue listed in a "Blocked by" section
3. Not already addressed by a recent `RALPH:` commit
4. Not a parent PRD, tracking issue, or umbrella issue unless it is the selected single issue and explicitly labeled `afk`

Priority order:

1. Unblocked PRD child slices labeled `afk`
2. Critical bugfixes
3. Tests for completed slices only when represented by a separate issue labeled `afk`

If all actionable tasks are complete, output exactly `<promise>COMPLETE</promise>` and stop.

# CODEBASE CONTEXT

This is Tack, a Go project.

- Main CLI: `cmd/tack/`
- Daemon: `internal/daemon/`
- Core domain/store/service logic: `internal/`
- Use Bun for JS tooling; never npm or pnpm
- Build: `go build ./...`
- Test: `go test ./...`
- Optional lint: `golangci-lint run` if available

# EXPLORATION

Before editing, inspect the relevant code and tests for the selected issue.

If the issue references a parent PRD or blocker issues, fetch them with `gh issue view <number> --comments`.

# EXECUTION

Complete only the selected issue's acceptance criteria.

Follow existing patterns:

- Keep tests in `*_test.go` beside source files
- Use `slog` for logging
- Follow existing store, daemon route, client, and CLI test patterns
- Prefer small, deep modules with stable interfaces over shallow helpers
- Do not add broad abstractions or future behavior outside the issue scope
- Do not implement HITL issues unless the issue is explicitly selected, labeled `afk`, and has enough detail to proceed without questions
- Do not mine parent PRDs for extra work after child issues are complete. If no eligible labeled issue remains, output `<promise>COMPLETE</promise>`.

# VERIFICATION

Run validation before committing:

1. `go build ./...`
2. `go test ./...`

If a narrower test fails while developing, fix it. The final state must pass the full build and test commands unless the issue explicitly scopes validation differently.

# COMMIT AND PUSH

After verification passes, commit and push.

1. Stage only relevant files.
2. Commit with a message that starts with `RALPH:` and references the issue number with `#N`.
3. Push to `origin main`.

Do not skip the push. The issue is not complete until the commit is pushed.

# ISSUE UPDATE

If the task is complete, close the original issue with `gh issue close <number>` and a concise completion comment.

If the task is partial, leave a comment with what was done, what remains, and any blocker.

# FINAL RULES

1. Pick exactly one issue.
2. Implement only that issue.
3. Run `go build ./...` and `go test ./...`.
4. Commit with `RALPH:` prefix and issue reference.
5. Push to `origin main`.
6. Close or comment on the issue.
7. Then output a concise final summary and exit.

Do not start a second issue. The loop script handles iteration.
