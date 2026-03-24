# ISSUES

Issues JSON is provided at start of context. Parse it to get open issues with their bodies and comments.

You've also been passed recent RALPH commits (SHA, date, full message). Review these to understand what work has been done.

# TASK SELECTION

Pick the next task. Only pick issues that are:

1. Marked **AFK** in the issue body (or clearly implementable without human input)
2. NOT blocked by any open issue (check "Blocked by" sections — if a blocker issue is still open, skip it)
3. Not already addressed by a recent RALPH commit

Priority order:

1. Critical bugfixes
2. Unblocked refactor slices (issues with no open blockers)
3. Tests for completed refactors
4. Polish and quick wins

If all actionable tasks are complete, output <promise>COMPLETE</promise>.

# CODEBASE CONTEXT

This is a Go project. The main CLI is `cmd/deck/`, daemon is `cmd/daemon/`. Core logic lives in `internal/`.

- Use **Bun** for any JS tooling (not npm/pnpm) — but this is primarily Go
- Build: `go build ./...`
- Test: `go test ./...`
- Lint: `golangci-lint run` (if available)
- Read the CLAUDE.md files at the repo root and parent directory for architecture rules

# EXPLORATION

Explore the repo and fill your context window with relevant information to complete the task. Read the CLAUDE.md files. If the issue references a parent PRD or linked issues, fetch them with `gh issue view <number>` for context.

# EXECUTION

Complete the task. Follow existing patterns in the codebase:

- Keep tests in `*_test.go` alongside source files
- Use `slog` for logging
- Follow existing test and mock patterns in the codebase
- Interfaces go in the same package as the primary implementation
- No unnecessary abstractions — only do what the issue asks for

# VERIFICATION

- Run `go build ./...` to verify compilation
- Run `go test ./...` for full test suite
- Ensure no regressions

# COMMIT

Make a git commit. The commit message must:

1. Start with `RALPH:` prefix
2. Reference the issue number(s) with `#N`
3. Summarize what was done
4. Note key decisions made
5. List any blockers or notes for next iteration

Keep it concise.

# THE ISSUE

If the task is complete, close the original GitHub issue with `gh issue close <number>`.

If the task is not complete, leave a comment on the GitHub issue with what was done using `gh issue comment <number> --body "..."`.

# FINAL RULES

ONLY WORK ON A SINGLE TASK.
