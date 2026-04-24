# External Tools Pilot

Aphelion now has a generic external-tool lane for agent-owned capabilities. Core
loads manifest JSON, enforces the governor lifecycle, executes supported
`process`/`subprocess` tools through the sandbox runner, and keeps browser or
domain behavior outside core.

## Bundled Pilot

The first pilot is `browse_page`, owned by `idolum-email`:

- manifest: `external-tools/browse_page/manifest.json`
- deterministic fixture entry: `external-tools/browse_page/bin/browse_page.sh`
- install probe: `external-tools/browse_page/bin/probe.sh`
- default exposure intent: `idolum-email` only

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

## Required Lifecycle

An external process tool becomes invokable only after this sequence succeeds:

1. `proposal_submit`
2. `proposal_ratify` or an explicit approved override
3. `install_set` to `pending`
4. `install_execute`
5. `audit_run`
6. `probe_run`
7. `install_set` to `verified`
8. `register`
9. `exposure_set`
10. invocation by an exposed principal

Verification requires runtime-authored `audit_run` and `probe_run` evidence.
Registration, exposure, manifest listing, `install_show`, `audit_show`, and
invocation recompute the verified fingerprint. Any manifest or entry-file drift
marks the install stale and blocks registration/execution until repaired and
reverified.

## Execution Modes

- `process` and `subprocess`: executable through the sandbox runner when
  constraints are supported.
- `container` and `workspace_runner`: importable and diagnosable, but not
  process-executable yet.

Unsupported modes must remain visible as non-executable manifest entries rather
than being falsely verified as process tools.
