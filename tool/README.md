# `tool/` boundary

`tool/` is Aphelion's governed tool-runtime facade. It translates typed tool
requests into constrained local behavior: native tool implementations,
capability authority surfaces, external manifest lifecycle, sandbox-aware
execution, and tool-facing render output.

The shortest boundary sentence is:

> `tool/` owns governed tool behavior and lifecycle evidence, not ambient
> permission.

A tool implementation existing in the repository is not enough to make it safe,
registered, exposed, granted, verified, or callable. Authority flows through
explicit state transitions that must remain reviewable and revocable.

## Owned responsibilities

Top-level `tool` owns behavior when it is about executing or presenting a tool
inside Aphelion's governed runtime, especially:

- native tool definitions and registry wiring;
- argument decoding and stable tool-result rendering;
- admin/governor-facing tools for plan, operation, proposal, memory, mission,
  artifact, file, network, and provider-backed operations;
- capability request/review/grant access checks and render surfaces;
- tool authority lifecycle actions for registration, install, audit, probe,
  drift, and status projection;
- external tool manifest loading, visibility, lifecycle command execution, and
  manifest-backed invocation checks;
- durable-agent tool surfaces for local governance, profile, memory, artifacts,
  lifecycle, conversations, snapshots, and delegation reports;
- sandbox-aware process execution through `tool/sandbox`;
- evidence recording for tool invocations and lifecycle transitions.

`tool/sandbox` is the lower-level process execution seam. It owns roots,
profiles, Linux exec behavior, and the narrow sandbox-network helper protocol.
It should stay below tool policy and runtime orchestration.

## Non-owned responsibilities

`tool` must not become the runtime shell, transport layer, or hidden authority
source. Code belongs elsewhere when it owns:

- turn stage order, governor/face sequencing, or commit/delivery orchestration;
- background scheduler loops, channel polling, service lifecycle, or process
  restart/deploy behavior;
- Telegram or other transport command semantics;
- storage schema/migrations outside durable record writes through `session`;
- policy or permission changes that bypass capability/tool authority state;
- external account use, credentials, purchases, public contact, deploys, or
  restarts without an active grant/lease;
- provider SDK behavior that should be isolated behind a narrower adapter.

If code needs `runtime`, `turn`, `pipeline`, or transport packages to decide
what happens next, it probably does not belong in `tool`.

## Authority and lifecycle contract

The lifecycle language is intentionally strict:

```text
request != review/approval != grant
registration != exposure != invocation
install != audit != probe != verified
manifest present != tool available
repo artifact != active capability
```

The key meanings are:

- **Request**: a durable ask for capability or delegation review. It is not a
  grant and cannot expose a tool by itself.
- **Grant**: a reviewed authority record that permits specific actions inside
  explicit constraints. A grant does not install or verify implementation bits.
- **Registration**: a known tool/lifecycle record. Registration does not imply
  exposure, invocation authority, install success, or safety.
- **Exposure**: inclusion in the model/tool surface. Exposure still requires the
  invocation path to enforce grants, runtime ceilings, and current lifecycle
  state.
- **Invocation**: the actual execution path. Invocation must re-check authority
  and constraints at the point of use.
- **Install**: environment setup or install evidence for an external tool.
  Install evidence alone is not runtime behavior proof.
- **Audit**: bounded evidence that the environment can resolve/load the tool or
  its declared command surface.
- **Probe**: bounded runtime behavior evidence against the declared probe path.
- **Verified**: current evidence is green for the active baseline. At minimum,
  install, audit, and probe evidence must match the current manifest/install
  reference/workspace baseline. Verification must not inherit stale confidence
  from old runs.
- **Drift**: any evidence that the active baseline no longer matches verified
  evidence. Canonical drift candidates include manifest hash drift,
  install-ref drift, workspace fingerprint drift, container/runtime identity
  drift, and failed reprobe. Drift should degrade confidence with an explicit
  stale reason.

## Current subsystem map

| Cluster | Representative files | Tool-owned role | Boundary pressure |
| --- | --- | --- | --- |
| Native registry and execution | `definitions.go`, `registry.go`, `exec.go`, `exec_runtime.go`, `exec_guard.go`, `native_file_tools.go`, `safe_write_linux.go` | Tool definitions, argument decode, guarded local execution, file/network/write constraints | Do not absorb turn orchestration or runtime shell behavior |
| Capability authority | `capability*.go`, `authority_access.go`, `capability_update_*.go` | Capability request/review/grant tool surfaces and access checks | Request must not become grant; grant must not bypass invocation checks |
| Tool authority lifecycle | `tool_authority*.go`, `tool_install_test.go` | Registration, install, audit, probe, drift/status lifecycle records and render output | Verified/drift semantics need evidence-based contracts |
| External manifests | `external_*.go`, `manifest*.go`, `configured_visibility.go` | Manifest-backed tool metadata, visibility, lifecycle execution, external invocation | Manifest present must not imply callable or safe |
| Durable-agent tools | `durable_agent*.go`, `durable_memory_approval.go`, `durable_snapshot_approval.go` | Governed child-agent administration, artifacts, profile, memory, conversations, lifecycle, delegation | Child policy/grants stay explicit; no authority by resemblance |
| Operation, plan, proposal tools | `update_operation*.go`, `update_plan*.go`, `request_approval.go`, `operation_artifact.go` | Durable operation/plan/proposal state tools and artifact resolution | Tool output must not masquerade as approval or deployment evidence |
| Memory and mission tools | `memory.go`, `mission_ledger.go` | Curated memory and mission-ledger tool access | Mission/self-summon is not permission to act |
| Provider-backed tools | `web_search.go`, `openai_storage.go`, `codex_image_generation.go` | Governed provider/native tool surfaces | Provider configuration is not an active grant |
| Remote host and tailnet tools | `remote_host.go` | Remote host/status/control tool surface | Tailnet reachability is transport, not mutating authority |
| Sandbox | `sandbox/*.go` | Roots, profiles, Linux exec, network helper | Stays below tool policy and runtime orchestration |

## Growth rules

A new file belongs in `tool` when most of these are true:

1. It implements a typed tool surface, tool registry behavior, tool invocation
   guard, or tool lifecycle/status rendering.
2. It enforces an authority or lifecycle check at the point of tool use.
3. It records durable tool evidence through `session` without owning session
   schema/migration policy.
4. It can be tested as tool behavior without constructing the whole runtime
   shell or Telegram transport.
5. It preserves the lifecycle distinctions above instead of compressing them
   into one boolean such as "available" or "safe".

Prefer another package when code is mostly turn orchestration, transport UX,
storage schema, provider SDK plumbing, runtime process supervision, or a reusable
non-tool domain model.

## Import direction

Good dependency direction:

```text
runtime/turn/transport  --->  tool/  --->  session/core/principal
                              tool/  --->  tool/sandbox
```

Forbidden dependency direction:

```text
tool/          -X->  runtime/
tool/          -X->  turn/
tool/          -X->  pipeline/
tool/          -X->  telegram/
tool/sandbox/  -X->  tool/
tool/sandbox/  -X->  runtime/turn/pipeline/telegram/session/
```

`tool/sandbox` may use lower-level principal/sandbox profile facts, but it must
not learn tool authority, session lifecycle, runtime orchestration, or transport
semantics.

## Cleanup posture

Do not extract `tool` opportunistically. The package is large because it is the
reviewable facade where many authority-bearing operations meet. The safe cleanup
sequence is:

1. keep request/review/grant/register/expose/invoke semantics explicit;
2. make verified/drift status evidence-based and staleable;
3. preserve `tool/sandbox` as the lower-level execution seam;
4. only then consider extracting cohesive helper packages whose APIs reinforce,
   rather than hide, the authority lifecycle.
