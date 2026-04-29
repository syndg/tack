# Contributing to Tack

Tack is in public alpha. Things will break. Things will change.

## Local Setup

Build the CLI from source:

```bash
go build -o bin/tack ./cmd/tack
```

Run the daemon from a separate terminal when testing workflow commands:

```bash
bin/tack daemon
```

Initialize a throwaway repository before testing agent workflows. Do not test first-run flows against a repository with uncommitted work you care about.

## Checks

Run these before opening a pull request:

```bash
go test ./...
```

Docs-site builds are handled by Vercel.

## Reporting Issues

- Open a GitHub issue.
- Include: what you did, what happened, what you expected.
- Include: `tack version`, OS, agent runtime, provider/model.
- Include whether you used local worktrees or Daytona sandboxes.
- Include relevant `tack watch --summary` output when possible.

Do not include API keys, OAuth tokens, daemon tokens, private repository code, or full activity logs that may contain sensitive content.

## Security Issues

Do not report vulnerabilities in a public issue with exploit details. Follow `SECURITY.md`.

## Pull Requests

- Open an issue first. Discuss the change before writing code.
- Keep PRs small and focused.
- Run `go test ./...` before submitting.
- Update docs when behavior or CLI output changes.
- Call out security, credential, sandbox, or config impacts explicitly.
- Follow existing code style. No comments unless necessary.

## Docs

Current public docs live in `docs-site/content/docs/`. Internal and historical docs live under `docs/`; check `docs/README.md` before treating an older plan as current product behavior.

## Code of Conduct

Be kind. Be constructive. Be respectful.

## License

By contributing, you agree that your contributions will be licensed under Apache-2.0.
