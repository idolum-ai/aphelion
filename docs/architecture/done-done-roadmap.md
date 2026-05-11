# Done-Done Roadmap

_Status: normative release-quality target._

This document defines the smaller "done done" target for Aphelion. Done-done no
longer means completing every research direction in the repo. It means the
current governed outpost is truthful, typed, recoverable, release-ready, and
small enough to keep operating.

## Surface Boundary

Operator and user surfaces are limited to:

- Telegram
- CLI commands

Aphelion must not add a web dashboard, private web UI, browser artifact
explorer, separate operator console, plugin marketplace, or omnichannel product
surface. Machine-to-machine protocols may exist only when required by a concrete
governed outpost workflow, and must project through Telegram, CLI, TES,
`/status`, `/debug`, and `/doctor`.

Future channels such as WhatsApp should be ordinary compiled-in code changes
behind a small transport boundary. They must not introduce a plugin system,
marketplace surface, or broad omnichannel abstraction.

## Done-Done Means

The release target includes:

- public docs, requirements, status surfaces, and promise ledger agree with the
  implemented system
- proposals, approval leases, grants, auto-approval, revocation, deploy/restart,
  and recovery have inspectable authority evidence
- `/status`, `/debug`, `/doctor`, Telegram, and CLI project typed records rather
  than inventing independent authority
- sandbox and network policy claims are honest: `deny` is enforced, unsupported
  allowlists fail closed or are clearly reported unavailable
- external tools remain narrow: verified `process`/`subprocess` manifests,
  sandboxed execution, install/audit/probe evidence, drift detection, rollback,
  and no generic marketplace substrate
- durable children remain bounded: declared identity, scoped credentials,
  scheduled/channel wakes, child-local state, upward review, and parent-visible
  health without remote fleet-control promises
- release, install, update, rollback, public-readiness, and architecture checks
  are reproducible from the repo

## Not Current Targets

These topics may remain as research notes, but they are not done-done release
requirements:

- broad Mission Control, autonomous mission self-continuation, or recurring
  missions that act without a separate lease/grant
- live Tailnet child `tsnet` materialization, live Tailscale ACL/grant mutation,
  public Serve/Funnel exposure, or Tailnet as an operator UI
- remote durable-child control planes with enrollment, key rotation, offline
  command queues, or fleet management
- generic external-tool platforms, container execution, workspace-runner
  execution, plugin ecosystems, or agent-owned tool marketplaces
- new dashboards, browser artifact explorers, enterprise consoles, or
  omnichannel abstractions

If one of these directions becomes necessary later, it must start from a concrete
governed outpost workflow, update `docs/promises.md`, and prove that the smaller
Aphelion shape still holds.

## Remaining Implementation Work

The current done-done worklist is intentionally short:

- keep the authority/status/doctor consistency projection and exact
  `authority repair --apply --finding` closures covered as new typed authority
  records are added
- keep sandbox network behavior truthful by either enforcing advertised policy
  or reporting unavailable enforcement before execution
- keep external-tool execution limited to verified, sandboxed
  `process`/`subprocess` manifests with drift and rollback evidence
- keep Tailnet and mission surfaces projected as current declarations/review
  only, not live mutation or autonomous continuation
- keep release docs, scripts, public notices, and static artifact checks aligned
  with the actual install/update/rollback path

## Delivery Rule

Work may be delivered as many reviewable commits, but there is only one final
release and one live migration/init call for done-done completion. In short:
one final release and one live migration/init call.

Before that release:

- unfinished behavior stays disabled, inert, or explicitly marked future
- schema work accumulates into one additive live migration path if any migration
  is required
- local tests may create and migrate throwaway databases, but the live service
  database is touched only once during the final release
- no partial release is treated as done-done

## Self-Validation Gates

Every commit group must pass the relevant focused tests. The final release
candidate must pass:

- `go test ./...`
- `make architecture`
- `make public-readiness`
- `make design-principles`
- `make deadcode`
- `make taste`
- `git diff --check`

The final release candidate must also prove:

- authority consistency issues are either repaired or surfaced by `/doctor`
- autonomy mode precedence and expiry remain bounded by config ceilings
- Telegram buttons and CLI labels are compact projections over typed records
- sandbox isolation and network policy behavior match visible status
- external-tool unsupported modes are not callable and explain why
- Tailnet and mission surfaces do not claim live mutation or self-continuation
- no unsupported execution claims appear in visible replies
- no web, dashboard, marketplace, or omnichannel operator surface exists

## Open-Source Readiness

Open-source readiness is a release blocker. The final system must have:

- no private paths, live credentials, personal fixtures, or tracked runtime
  state
- complete install, update, rollback, and verification docs
- public issue, security, contribution, license, and third-party notices
- reproducible static release artifacts
- docs that clearly distinguish implemented, planned, future, and research-only
  behavior
- structural checks that keep broad operational files and broad roadmap promises
  from growing back

## Research Notes

The following documents are design inputs only. Their targets are normative only
where they are incorporated into this release target or `docs/promises.md`:

- `canonical-state-and-autonomy-roadmap.md`
- `mission-ledger-roadmap.md`
- `organic-agent-owned-tools-proposal.md`
- `tailscale-agent-substrate-project.md`
- `agent-authority-ledger.md`
