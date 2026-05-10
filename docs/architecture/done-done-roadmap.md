# Done-Done Roadmap

_Status: normative convergence plan._

This document defines what it means for Aphelion to be "done done": the repo's
requirements, promise ledger, and draft roadmaps have converged into one
implemented, tested, open-source-ready system without relaxing the design
principles.

## Surface Boundary

Operator and user surfaces are limited to:

- Telegram
- CLI commands

Aphelion must not add a web dashboard, private web UI, browser artifact
explorer, or separate operator console. Tailnet and internal RPC may exist for
machine-to-machine child control, health, and policy protocols, but those
protocols are not operator surfaces. They must project through Telegram, CLI,
TES, `/status`, `/debug`, and `/doctor`.

Future channels such as WhatsApp should be ordinary compiled-in code changes
behind a small transport boundary. They must not introduce a plugin system,
marketplace surface, or broad omnichannel abstraction.

## Completion Shape

Done-done completion includes:

- canonical authority accounting across proposals, leases, grants,
  auto-approval, revocation, deploy/restart, and recovery
- configurable autonomy where static config sets ceilings and Telegram can
  create scoped live overrides within those ceilings
- Mission Control with working objectives, durable missions, self-summon as
  review, bounded self-continuation, budgets, decay, and restart handoff/result
  evidence
- durable children with signed internal control RPC, replay protection, policy
  versioning, enrollment, rotation, revocation, offline queueing, and bootstrap
  ceiling enforcement
- agent-owned external tools with governed install, attestation, probe, audit,
  drift detection, rollback, and generic execution
- Tailnet support for private machine reachability, child identities, grant
  binding, policy projection, approval-gated mutations, and diagnostics, all
  controlled from Telegram and CLI
- provider parity, cache behavior, sandbox hardening, config warnings,
  anonymization, observability, backup/restore, disaster tooling, docs, release
  automation, and public-readiness closure

## Delivery Rule

Work may be delivered as many reviewable commits, but there is only one final
release and one live migration/init call for done-done completion. In short:
one final release and one live migration/init call.

Before that release:

- unfinished behavior stays disabled, inert, or hidden behind explicit config
  gates
- schema work accumulates into one additive live migration path
- local tests may create and migrate throwaway databases, but the live service
  database is touched only once during the final release
- no partial release is treated as done-done

If an implementation would require several incompatible live migrations, it
must be redesigned into one additive migration plus post-migration
reconciliation.

## Self-Validation Gates

Every commit group must pass:

- `go test ./...`
- `make architecture`
- `make public-readiness`
- `make design-principles`
- `make deadcode`
- `make taste`
- `git diff --check`

The final release candidate must also prove:

- authority ledger consistency in `/doctor`
- autonomy mode precedence and expiry
- compact Telegram buttons and natural-language labels
- CLI status/doctor/repair output
- durable-child signed envelope and replay rejection
- sandbox isolation and network policy behavior
- Tailnet mutation approval and rollback paths
- no unsupported execution claims in visible replies
- no web or dashboard operator surfaces

## Open-Source Readiness

Open-source readiness is a release blocker. The final system must have:

- no private paths, live credentials, personal fixtures, or tracked runtime
  state
- complete install, update, rollback, and verification docs
- public issue, security, contribution, license, and third-party notices
- reproducible static release artifacts
- docs that clearly distinguish implemented, planned, and future behavior
- structural taste checks that keep broad operational files from growing back

## Roadmap Inputs

The following draft architecture documents remain design inputs. Their targets
are normative only where they are incorporated into this convergence plan or
the requirements/promise ledgers:

- `canonical-state-and-autonomy-roadmap.md`
- `mission-ledger-roadmap.md`
- `organic-agent-owned-tools-proposal.md`
- `tailscale-agent-substrate-project.md`
- `agent-authority-ledger.md`
