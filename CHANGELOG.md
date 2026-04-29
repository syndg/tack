# Changelog

## v0.1.0-alpha.1 - Unreleased

First public-alpha release of Tack.

### Added

- Objective-to-PR workflow with discovery, planning, human approval, parallel stream execution, quality gates, review, merge queue, and PR creation.
- Local git worktree sandboxes and Daytona VM sandbox support.
- Pi runtime as the recommended first-class agent runtime.
- Minimal Claude Code compatibility path.
- YAML blueprint workflows with deterministic and agentic steps.
- Scoped project rules for file-pattern-specific agent guidance.
- File-attribution quality gates to avoid blocking on unrelated pre-existing errors.
- Recovery loops for quality-gate failures, review rejections, transient runtime failures, and blocked human-guided retries.
- Live observability with `tack watch`, `tack logs`, `tack agents`, `tack mail`, and `tack merge`.
- Binary-first release packaging with GitHub Releases, Homebrew tap, install script, `go install`, and source build fallback.
- Public docs, security model, contribution guide, issue templates, and release workflow.

### Known Limitations

- Public alpha: APIs, config, and workflow details may change.
- Local sandboxes are git worktrees, not containers or VMs.
- Pi is the primary tested runtime; Claude Code is minimal compatibility.
- Docker, E2B, and semantic AI merge are planned but not included in this release.
- First-run success depends on correct provider credentials, git credentials, and project setup commands.
