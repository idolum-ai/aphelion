# Contributing

Aphelion is intentionally small: a Linux-first, Telegram-controlled, governed
outpost for personal agents. Contributions should preserve that shape.

The practical contributor path is
[docs/guides/contributor-handbook.md](docs/guides/contributor-handbook.md).

## Local Setup

```bash
go test ./...
make architecture
make build
```

Use `config.example.toml` as a reference. Do not commit live config, tokens,
session databases, logs, transcripts, or local artifacts.

## Design Rules

- Keep Aphelion an outpost, not a platform.
- Telegram is the primary radio link; avoid channel abstraction unless a concrete
  governed workflow needs it.
- Text is presentation. Authority, consent, leases, grants, and evidence should
  be typed records or compiled contracts.
- Prefer durable concepts and stable repeated behavior over one-off structures.
- Update architecture docs when changing `runtime`, `turn`, `pipeline`,
  `session`, or `durableagent` ownership boundaries.

## Pull Requests

Before opening a PR, run:

```bash
make public-readiness
make architecture
go test ./...
make build
git diff --check
```

If `gitleaks` is available, also run:

```bash
gitleaks detect --source . --no-git --redact
```

For security-sensitive changes, describe the authority boundary being changed and
the tests that prove it remains bounded.
