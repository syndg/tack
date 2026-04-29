# Security Policy

Tack runs LLM-directed commands near source code, git state, and credentials. Treat security reports seriously and avoid public disclosure before maintainers can investigate.

## Supported Versions

Tack is currently public alpha. Security fixes target the `main` branch until tagged releases are formalized.

## Reporting a Vulnerability

Prefer GitHub private vulnerability reporting if it is enabled for the repository.

If no private reporting channel is available, open a minimal public issue that says you have a security report and need a private maintainer contact. Do not include exploit details, tokens, private repository names, logs, or proof-of-concept payloads in the public issue.

Include privately:

- Affected Tack version or commit.
- Operating system and sandbox provider (`local` or `daytona`).
- Agent runtime and provider, if relevant.
- Minimal reproduction steps.
- Impact and suggested fix, if known.

## Security Model

Local sandboxes use git worktrees and run as your user. They are not containers or VMs. File scope is an orchestration and tool-policy boundary, not a kernel-enforced boundary.

Use Daytona sandboxes when you need VM-level isolation.

See `docs-site/content/docs/security.mdx` for the current security model and audit links.
