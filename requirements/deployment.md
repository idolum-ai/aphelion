# Deployment — Releases, Services, Updates, and Recovery

## Overview

Aphelion should be easy to run as a long-lived local or server-side system tool.

The default deployment story should be:

- prebuilt binaries published via GitHub Releases
- simple source build path for developers
- `systemd` service support for normal operation
- straightforward update and rollback flow

This is closer to the practical Codex-style binary distribution model than to a source-only workflow.

## Scope

### v0 required

- source build path
- local executable path
- user-level `systemd` service
- logs and restart behavior
- documented update flow

### Deferred after v0

- GitHub Releases automation
- signed artifacts
- distro packages
- container images
- release channels (`stable`, `edge`)

## Distribution Model

Aphelion should support two installation paths.

### Source install

For developers and contributors:

- clone repo
- build binary locally
- run directly or install service

### Release install

For operators and ordinary users:

- download the correct binary from GitHub Releases
- place it on `PATH` or under a managed install directory
- point it at `~/.aphelion/aphelion.toml`

GitHub Releases should be the preferred binary-distribution mechanism once release automation exists.

## Binary Artifacts

Release artifacts should be simple and boring.

Recommended first targets:

- Linux `x86_64`
- Linux `arm64`

Each release should ship:

- one binary per platform
- checksums
- concise release notes

Optional later additions:

- detached signatures
- provenance/attestation

## Runtime Modes

Aphelion should support:

- foreground interactive run
- user service
- system service

### Foreground

Best for:

- first-run setup
- debugging
- local smoke tests

### User service

Best default for:

- personal workstation use
- single-user server environments

### System service

Best for:

- shared hosts
- boot-time startup
- managed server installs

System service mode should be explicit and not the only supported deployment path.

## `systemd`

`systemd` should be the primary service manager on Linux.

The service contract should include:

- configured working directory
- explicit config path
- restart-on-failure
- clear stdout/stderr routing to journald
- environment isolation as needed

The user-service path should be the simplest supported install:

- install unit
- `systemctl --user enable --now aphelion`

## Updating

Updates should be operationally simple.

At minimum, Aphelion should support:

- pull/build/restart from source
- download/replace/restart from release binaries

The update path should be one command or one short script, not a hand-maintained ritual.

Typical source update flow:

1. update repo
2. rebuild binary
3. restart service

Typical release update flow:

1. fetch new binary
2. replace installed binary atomically
3. restart service

## Rollback

Rollback should be boring.

A practical first rollback model is:

- keep the previous binary
- restart service against the previous binary if the new release fails

The deployment spec should leave room for a fuller release manager later, but the first rollback story does not need to be elaborate.

## Config and State

Deployment must not confuse:

- executable location
- config location
- runtime state location
- workspace location

At minimum, these should stay clearly separated:

- binary
- `~/.aphelion/aphelion.toml`
- session DB
- workspace files

Updates should not overwrite operator-edited config or workspace files.

## Logs and Observability

Operators need a clear default place to look.

For `systemd` deployments, the primary log path should be journald.

The deployment story should document:

- service status
- live logs
- restart commands
- binary version checks

## Failure and Recovery

Deployment should assume:

- provider errors happen
- config mistakes happen
- bad releases happen
- service restarts happen
- in-flight turns may be interrupted by deploys or operator restarts

The deployment model should therefore prefer:

- restart-on-failure
- durable session DB
- structured interruption records
- stateless binary replacement
- operator-visible failure logs

A restart should not silently erase knowledge of interrupted work. The runtime should retain machine-authored facts about in-flight turns so the governor can analyze them after startup.

## Config Surface

Deployment itself should stay light on config, but the surrounding system should reserve room for:

```toml
[deployment]
service_mode = "user"        # user | system | foreground
install_method = "release"   # release | source
```

This section may remain mostly documentary at first; the important thing is preserving the shape.

## Decisions

- **GitHub Releases are the preferred binary channel.** Source builds remain supported.
- **Linux + `systemd` is the primary deployment target.** User services come first.
- **Updates should be one short path.** The operator should not need bespoke manual steps.
- **State and binary stay separate.** Updating Aphelion must not rewrite config or workspace files.
- **Rollback should be practical.** Keeping the previous binary is enough for the first real recovery story.

## Test Plan

- **TestBinaryRunsWithExplicitConfigPath**: built binary starts with `--config`
- **TestUserServiceUnitUsesConfiguredBinaryAndConfig**: service file points at the expected paths
- **TestRestartOnFailurePolicyPresent**: service definition restarts failed processes
- **TestUpdateScriptRebuildsAndRestarts**: source update path rebuilds and restarts service
- **TestReleaseInstallPreservesConfigAndWorkspace**: replacing the binary does not overwrite state/config surfaces
- **TestRollbackToPreviousBinary**: operator can restart the service against a previous binary
