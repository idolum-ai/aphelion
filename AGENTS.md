# Aphelion Engineering Guide

Aphelion is a governed outpost for personal agents. Keep changes aligned with
the project principles in
[`docs/architecture/design-principles.md`](docs/architecture/design-principles.md).
That file is the canonical source for principle naming and order.

## Structure

- Root package: single-binary composition, CLI command dispatch, deploy/install
  entrypoints, and Telegram UI glue.
- `runtime`: long-lived house shell, transport wiring, locks/scopes, background
  loops, durable-agent lifecycle wiring, and concrete port assembly.
- `turn`: one-turn state machine, stage ordering, run-kind policy, and
  commit/delivery contracts.
- `pipeline`: governor/face conversational transforms and render/floor contract
  helpers.
- `session`: durable storage records and persistence APIs.
- `tool`: bounded tool implementations and sandbox integration.
- `telegram`: Telegram transport client and wire-level Telegram types.
- `durableagent`: child-agent substrate, policy, enrollment, and forensics.

## Boundary Rules

- Do not grow Aphelion toward plugin-marketplace or omnichannel structure unless a
  concrete governed outpost workflow requires it.
- Add structure only for durable concepts or repeated stable behavior. Avoid new
  packages for one-off cases.
- Runtime may assemble concrete dependencies, but lower packages should not import
  upward into `runtime`.
- `turn` should not know about Telegram, provider clients, tools, or process-shell
  wiring.
- `pipeline` should stay focused on conversational transformations, not storage,
  transport, tools, or stage sequencing.
- Text is presentation. Authority, consent, leases, grants, and evidence should be
  typed records or compiled contracts.

## Local Verification

- Run `make architecture` when changing package boundaries or architecture docs.
- Run `go test ./...` for behavioral changes.
- Run `make design-principles` when touching authority, consent, continuation,
  wake, goal, status, or operator-facing control surfaces.
