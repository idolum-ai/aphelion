# External Tools Pilot

Aphelion now has a generic external-tool lane for agent-owned capabilities. Core
loads manifest JSON, enforces the governor lifecycle, executes supported
`process`/`subprocess` tools through the sandbox runner, and keeps browser or
domain behavior outside core. This is not a plugin marketplace or a general
tool platform; unsupported execution modes remain non-callable.

For the package-local orientation and newcomer map, see
[`external-tools/README.md`](../../external-tools/README.md).

## Bundled Pilot

The first pilot is `browse_page`, owned by `child-alpha`:

- manifest: `external-tools/browse_page/manifest.json`
- deterministic fixture entry: `external-tools/browse_page/bin/browse_page.sh`
- install probe: `external-tools/browse_page/bin/probe.sh`
- first intended grant target: `child-alpha`

The bundled implementation is intentionally a deterministic fixture. It proves
the governed external-tool lifecycle in CI without adding browser dependencies
or page-fetching logic to Aphelion core. A real browser-backed implementation
should replace the external script/container behind the same manifest contract,
not add browser special cases to `tool/` or `runtime/`.

## Runtime Loading

External manifests are loaded from:

```toml
[tools]
external_manifest_dir = "/path/to/aphelion/external-tools/browse_page"
```

The directory loader reads `*.json` files directly under that directory. To load
the bundled pilot, point `external_manifest_dir` at
`/path/to/aphelion/external-tools/browse_page`.

## Canonical Lifecycle Contract

The external-tool lifecycle has one canonical flow:

1. `capability_request` (`kind=tool`)
2. install (`install_set pending` plus `install_execute`, or an operator-owned
   equivalent that records `install_ref`)
3. audit (`audit_run`)
4. verify (`probe_run` plus `install_set verified`)
5. register
6. grant
7. invoke

Each phase has a bounded claim:

- proposal: a tenant, agent, or operator requested a tool capability through
  `capability_request` and named the desired contract; it does not imply
  safety, installability, or availability.
- install: an operator provisioned or referenced an artifact with an
  `install_ref`; it does not imply the runtime can load it.
- audit: `audit_run` is the only import/load attestation surface. It proves the
  runtime can resolve the declared entry, discover the interpreter or container
  identity, and complete bounded loadability checks. It does not prove behavior.
- probe: `probe_run` proves declared behavior against the current install
  baseline. It does not replace audit.
- verify: `install_set status=verified` is the only source of truth for
  "verified." It requires fresh runtime-authored `audit_run` and `probe_run`
  records whose anchors match the current install baseline.
- register: the verified implementation becomes a named runtime capability. It
  does not grant access.
- grant: `capability_authority` gives a principal an active `kind=tool`
  grant with the `invoke` action. It does not skip freshness checks.
- invoke: the granted principal may call the tool only if the verified baseline
  is still fresh and the runtime policy ceilings are enforceable.

The canonical drift anchors are:

- `install_ref`: the operator-owned install artifact, image, path, or package
  reference.
- manifest hash: the normalized functional manifest contract.
- workspace fingerprint: process/subprocess local entry and command files, or
  container image/build/health identity for container tools.

Verified tools automatically become `stale` when any anchor moves. Drift reasons
are typed as `install_ref_changed`, `manifest_drift`, `workspace_drift`,
`container_drift`, `missing_baseline`, `fingerprint_error`,
`policy_violation`, `audit_failure`, `probe_failure`, `rollback`, or `removal`.
Stale tools cannot be registered, listed as callable for a principal, or invoked
until re-audited, re-probed, and re-verified.

Tenants and agents use `capability_request` with `kind=tool` for proposal
creation. Operators use `capability_authority` for parent/admin review and
admin grants, and `tool_authority` for tool install, audit, verification, and
registration. This keeps request attribution visible without handing lifecycle
authority to the requester.

Operators also use `tool_authority` `rollback` and `uninstall` actions to retire
manifest-backed tools. These actions optionally run manifest-declared rollback or
uninstall commands, disable the registered tool, revoke active tool capability
grants, mark install evidence stale, and persist TES events for rollback/removal,
registration change, grant revocation, and install-state change.

## Execution Modes

- `process` and `subprocess`: executable through the sandbox runner when
  constraints are supported. Network must currently be empty/`none`; filesystem
  must currently be empty/`none`; duration ceilings are enforced at execution
  time for install, audit checks, probe, and invocation.
- `container`: not process-executable, but has separate audit/drift semantics
  based on image, digest/build ref, and optional health check.
- `workspace_runner`: importable and diagnosable, but not executable yet.

Unsupported modes must remain visible as non-executable manifest entries rather
than being falsely verified as process tools.
