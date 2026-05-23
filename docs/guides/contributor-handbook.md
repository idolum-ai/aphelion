# Contributor Handbook

Aphelion is a small governed outpost, not a platform. Contributions should make
the live operator path more legible, bounded, recoverable, or durable.

Start with:

- [Design Principles](../architecture/design-principles.md)
- [Architecture Map](../architecture/README.md)
- [Package Ownership](../architecture/package-ownership.md)
- [Telegram UI Features](../telegram-ui-features.md)
- [Requirements Index](../../requirements/INDEX.md)

## Package Shape

- Root package: single-binary composition, CLI command dispatch,
  deploy/install entrypoints, and Telegram UI glue.
- `runtime`: long-lived house shell, transport wiring, locks/scopes,
  background loops, durable-agent lifecycle wiring, and concrete port assembly.
- `turn`: one-turn state machine, stage ordering, run-kind policy, and
  commit/delivery contracts.
- `pipeline`: governor/face conversational transforms and render/floor contract
  helpers.
- `session`: durable storage records and persistence APIs.
- `tool`: bounded tool implementations and sandbox integration.
- `telegram`: Telegram transport client and wire-level Telegram types.
- `durableagent`: child-agent substrate, policy, enrollment, and forensics.

Do not import upward into `runtime`. Keep `turn` unaware of Telegram, provider
clients, tools, and process-shell wiring. Keep `pipeline` focused on
conversational transformations, not storage, transport, tools, or stage
sequencing.

## Change Discipline

- Add structure only for durable concepts or repeated stable behavior.
- Keep files small enough to review without losing the package shape.
- Prefer typed records over prose for authority, consent, leases, grants, and
  execution evidence.
- Prefer projections over duplicate truth stores.
- Keep Telegram as the primary operator channel unless a concrete governed
  outpost workflow needs a compiled-in adapter.
- Do not keep temporary plans, stale notes, or private runtime artifacts in the
  repo.
- Update user docs when a user-facing command, button, config key, or service
  behavior changes.

## Local Loop

On Linux, run the full loop:

```bash
go test ./...
make architecture
make design-principles
make public-readiness
make build
git diff --check
```

Aphelion is Linux-only. On macOS or another non-Linux host, `make test` and
`make architecture` intentionally stop with a clear Linux-only message instead
of surfacing partial build-tag failures. Use the compile-only check locally, then
run the full loop on Linux before merge:

```bash
make verify-linux-compile
```

Use `make architecture` when changing package boundaries or architecture docs.
Use `make design-principles` when touching authority, consent, continuation,
wake, goal, status, or operator-facing control surfaces.

If `gitleaks` is available:

```bash
make secrets
```

## Docs Contract

User-facing surfaces should have both a guide path and a reference path.

- Guides teach what to do and what to check next.
- References define command surfaces, button surfaces, contracts, and package
  boundaries.
- Requirements define expected component behavior.
- Architecture docs explain how current code satisfies the requirements.

When a Telegram command changes, update the command registry, tests, the
Telegram UI reference, and any guide whose workflow changed.

When a config key changes, update `config.example.toml`, config tests, and the
operator setup guide if operators need to touch it.

## Review Standard

Before committing, inspect the diff for:

- public-safe docs and examples
- no live tokens, private paths, databases, logs, or transcripts
- command names that match the current registry
- bounded authority behavior with tests where behavior changed
- no broad abstractions that pull Aphelion toward marketplace, dashboard, or
  omnichannel shape
