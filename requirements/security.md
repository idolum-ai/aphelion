# Security — Trusted Admin Floor, Isolation Boundaries, and Hardening

## Overview

Security in Aphelion is not a prompt feature. It is a runtime property enforced below the prompt layer.

This document defines the security floor for each stage of the system:

- who may enter
- what code may execute
- what storage roots are visible
- what the prompt may and may not influence
- what the host must enforce in code

Security is staged on purpose. Aphelion must be honest about the difference between:

- a trusted admin tool runtime
- an actually isolated multi-user system
- borrowed external credentials
- Aphelion-owned credentials

## Core Principle

Prompt text may describe policy, but prompt text must never be the source of truth for security.

The real enforcement boundary lives in:

- principal resolution
- tool registry and dispatch
- root resolution
- process launcher and sandbox
- durable audit

## Threat Model

Aphelion is not defending against a hostile internet by default. It is defending against:

- accidental overreach by the governor
- drift caused by editable prompt files
- unsafe access by non-admin approved users
- secret exposure through tools
- host damage caused by unrestricted shell execution

The model should assume:

- admin is trusted but fallible
- approved users are semi-trusted and must be isolated
- prompt files may drift
- tools are the main danger surface

## Security Layers

There are five layers.

### 1. Admission

Unknown users must be denied before session creation.

The source of truth is config-owned Telegram principals:

- `admin`
- `approved_user`
- implicit deny for everyone else

### 2. Authority

Authority is resolved from the admitted principal, not from prompt text.

Prompt files may explain authority, but may not widen it.

At minimum:

- `admin` may operate on global roots
- `approved_user` may only operate on isolated roots
- `Idolum` has no authority of its own

### 3. Storage Isolation

All tool execution and file-oriented behavior must resolve against explicit roots:

- `global_root`
- `shared_memory_root`
- `user_workspace_root/<principal>`
- `user_memory_root/<principal>`

No operation may rely on an unresolved host path as if it were safe by convention.

### 4. Process Isolation

Actual safety depends on the launched process environment.

Relevant controls include:

- working directory
- read-only vs read-write mounts
- hidden paths
- Linux capabilities
- cgroup limits
- namespace isolation
- network policy

`workdir` restriction alone is not a sandbox.

### 5. Audit

Every security-relevant action should leave durable evidence:

- admission decision
- tool invocation
- sandbox profile used
- resolved roots
- exit status and truncation
- review-event generation and delivery

## Trusted-Admin Security Floor

The trusted-admin floor is:

- admitted Telegram principals only
- trusted admin execution only
- no non-admin security claims beyond configured sandbox readiness

### Required properties

- unknown Telegram users denied before session creation
- `admin` role resolved in code
- `Idolum` has no tool authority
- prompt files cannot widen permissions
- tool calls are persisted in the session ledger
- trusted `exec` is available only to the admin path

### Explicit non-goals

- no claim that `exec` is a real sandbox
- no claim that `workdir` restriction alone isolates the host

The current trusted `exec` implementation is a local shell convenience for the
service user. It is useful, but it is not sufficient isolation for non-admins.

## Isolation Floor

Non-admin and durable execution require a stronger floor:

- isolated writable roots per approved user
- global persona and shared memory exposed read-only
- hidden secret paths
- dropped dangerous capabilities
- resource limits
- default network deny or explicit allowlist
- namespace-backed process isolation

Approved-user execution must stay disabled until that floor is real.

## Prompt Boundary

Editable workspace files may change identity and style, but not machine-owned reality.

### Prompt files may influence

- tone
- priorities
- behavioral norms
- operator preferences

### Prompt files may not influence

- principal role
- tool availability
- writable vs read-only roots
- hidden paths
- network access
- sandbox profile

This is the basis for the system’s constitutional design:

- editable soul
- non-editable floor

## Secrets

Secrets should be treated as host-level assets, not conversational context.

Minimum requirements:

- config file path should be hidden from non-admin execution
- home-secret paths such as `~/.ssh` and `~/.gnupg` should be hidden from non-admin execution
- Codex credential files such as `CODEX_HOME/auth.json` or `~/.codex/auth.json` should be treated as secret material
- configured GitHub App private key files and minted installation tokens should be treated as secret material
- secret material must never be injected into prompts
- tool output should be assumed sensitive and stored durably but carefully

Later hardening may add credential sealing, restricted environment propagation, and stronger runtime secret handling.

## Network Policy

Network access should be specified per profile.

### Trusted admin

- trusted admin execution may use host networking

### Isolated execution

- approved-user execution should default to `deny`
- explicit allowlist requires a host-enforced backend and explicit destinations
- firewall and namespace policies should be machine-enforced, not prompt-described

For isolated `allowlist`, each destination must include an explicit port.
Process execution compiles hostname entries to IP/port rules at run start and
uses the helper-backed Linux netns+nftables backend when available. The user
service must not need host network namespace or firewall capabilities for this
path; those live behind the narrow `sandbox-net helper serve` socket. The
current backend enforces IPv4 egress and must fail closed for IPv6-only
destinations. If the backend is not available, execution must fail closed.
Native `fetch_url` applies the same destination ceiling in-process. Hostname
entries are resolved to IP/port destinations; each request and redirect must
resolve to an allowed destination before the tool dials that address. This is
not HTTP Host or TLS SNI identity policy. For non-admin principals, native
`fetch_url` also refuses host-private and special resolved destinations,
including loopback, link-local, private/ULA, multicast, unspecified, and
Tailnet CGNAT ranges.

## Sandbox Backends

Aphelion should preserve a stable sandbox contract while allowing stronger backends later.

Possible implementation paths:

- native Linux isolation
- a stronger backend such as `runsc`

The rest of the system should depend on the sandbox contract, not a single backend implementation.

## Failure Model

Security denial is a normal runtime outcome.

Examples:

- unknown principal denied
- tool unavailable for role
- path outside allowed roots
- hidden path access denied
- sandbox profile denies network
- command terminated for timeout or resource limit

These should become explicit tool or runtime errors, not silent fallthrough.

## Config Surface

Security-relevant config lives primarily in:

- `principals.*`
- `sessions.isolation`
- `sandbox.*`
- role-specific sandbox profiles
- governor backend selection
- governor auth source selection
- review delivery policy

Config may describe the intended security shape, but runtime must not silently pretend a profile is enforced when it is not.

## Decisions

- **Security is below the prompt layer.**
- **Trusted admin execution is not a sandbox.**
- **`workdir` is not isolation.**
- **Approved-user tools stay off until the isolation floor is real.**
- **Editable identity is allowed. Editable permissions are not.**
- **The important boundary is editable soul vs non-editable floor.**
- **Borrowed governor credentials are still secrets.**

## Test Plan

### Admission and Authority

- **TestUnknownPrincipalDeniedBeforeSessionCreation**: unknown Telegram user is denied before session load
- **TestIdolumCannotInvokeTools**: face layer never receives tool authority
- **TestPromptCannotOverrideRole**: workspace prompt files cannot widen principal authority

### Trusted Admin Floor

- **TestAdminExecAvailable**: trusted admin can invoke `exec`
- **TestApprovedUserExecDisabledUntilIsolationReady**: non-admin tool execution remains disabled before the isolation floor is enabled
- **TestToolCallIsPersisted**: admin tool calls leave durable session evidence

### Isolation Floor

- **TestResolvedRootsMatchPrincipalRole**: admin and approved-user roots differ as configured
- **TestApprovedUserCannotWriteGlobalRoots**: isolated profile denies writes to global workspace/shared memory
- **TestHiddenPathsDenied**: configured secret paths are inaccessible to approved-user execution
- **TestNetworkDenyProfile**: approved-user profile denies outbound network when configured
- **TestSandboxFailureIsExplicit**: denials return explicit tool/runtime errors rather than silent success

### Drift Resistance

- **TestToolsMDIsAdvisoryOnly**: prompt changes do not widen tool permissions
- **TestIdolumFilesDoNotAffectSandboxPolicy**: `IDOLUM.md` and `QUESTIONS-TO-IDOLUM.md` cannot change security behavior
