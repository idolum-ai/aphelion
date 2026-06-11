# Tools

## Goal

Tools let the agent act on the host in bounded, inspectable ways.

Aphelion treats tools as a **system surface**, not just an LLM convenience:

- tool availability is defined in code
- tool behavior is enforced by code and config
- tool policy may be explained to the model via prompt text
- prompt text is never the source of truth for security

The same is true for tool self-awareness: the model should be told its actual tool surface by machine-generated manifest, not by inference or stale prose. See `self-awareness.md`.

Current implemented tool surface:

- admin `exec`
- scoped native file/search/fetch tools
- curated memory and session recall
- optional OpenAI storage/vector tools
- role-aware sandbox policy for non-admin and durable execution
- narrow external `process`/`subprocess` manifests behind install/audit/probe,
  grant, drift, and rollback checks
  (see [`external-tools/README.md`](../external-tools/README.md) for the
  package-local orientation)

The security floor matters here:

- admin `exec` is a trusted-admin tool
- non-admin and durable tool execution must use configured scoped roots and
  sandbox readiness checks
- unsupported sandbox/network policy must fail closed or be reported before
  execution, not silently ignored

Semantic retrieval belongs in the later tool surface, not in ambient prompt assembly. It should be reachable deliberately by the governor as a retrieval tool, not silently injected.

## Design Lineage

Aphelion's tool model should be read against two nearby patterns:

- **OpenClaw**
  - layered per-run tool policy
  - owner-only tools
  - sandbox-aware filtering
  - lighter tool/context surfaces for cron and subagent runs
- **Hermes**
  - central registry and toolset model
  - simpler global availability model
  - strong special-case restrictions for delegated children

Aphelion should end up closer to OpenClaw on **enforcement shape**, but with a different constitutional split:

- `Idolum (System)` governs tool availability and side effects
- `Idolum` never owns tools
- principal role and run kind are first-class manifest inputs

So the intended synthesis is:

- OpenClaw-style layered runtime enforcement
- Hermes-style registry clarity
- governor/face and principal/isolation boundaries inside the service harness

The canonical attribution and departure record is
[`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md).

## Philosophy

1. **Reality in code.** The registry, schemas, sandbox, and permissions live in Go.
2. **Policy in prompt text.** `TOOLS.md` may explain style, risk posture, and preferred usage to the model.
3. **Least privilege.** Non-admin sessions only get tools and writable roots needed for their isolated scope.
4. **Audit first.** Every tool call is durable session data.
5. **One registry.** The agent sees a single normalized tool surface even if implementation backends differ.
6. **Governor-only execution.** The face never receives tools or authority to invoke them.
7. **Per-run truth.** The manifest may change by principal, session kind, and run kind; stale static tool lists are not acceptable.
8. **Readable status.** User-visible progress derived from tools should summarize intent and phase, not dump raw payloads by default.

## Tool Layers

There are four distinct layers:

1. **Registry**
   - Declares tool names, schemas, and dispatch handlers.
2. **Enforcement**
   - Validates inputs, resolves roots, applies sandbox profile, runs the tool, truncates output, and records errors.
3. **Manifest**
   - Machine-generated summary of the actual registered tools and current role-specific constraints.
4. **Policy Overlay**
   - Optional workspace-authored `TOOLS.md`, appended to the prompt to describe operator preferences or local norms.

Only the first two layers are authoritative.

The key difference from a simpler Hermes-style registry is that Aphelion's authoritative surface is resolved **per run**, not just per process.

## Confirmation, Proposals, and Escalation

Aphelion should split "asking before acting" into three different mechanisms rather than flattening them into one generic approval queue.

### 1. Conversational confirmation

This is prompt-level behavior.

The governor should ask the user for a plain yes/no style confirmation when:

- authority genuinely depends on it
- intent is materially ambiguous
- a destructive or irreversible action is next

This is not a security boundary. It is a turn-shaping rule.

### 2. Runtime proposal gate

This is code-level enforcement.

When the governor reaches a material threshold in the operation, the runtime may stop and require an explicit proposal approval before execution continues.

Examples:

- capability acquisition such as dependency installation
- networked or external operations
- destructive or irreversible mutations
- interruption decisions while another turn is still active

This must not live only in prompt prose. The runtime needs a real pending-decision object, an explicit user response path, and durable session-native proposal state.

### 3. Escalation

This is an authority boundary.

If a requested action exceeds the current principal's authority, the system should not ask that same principal to "approve" it into existence. It should deny or escalate into the appropriate review path instead.

That distinction matters:

- confirmation = clarify or confirm intent
- proposal = approve a bounded package of materially consequential work
- escalation = request ratification from a higher-authority path

Aphelion should keep those concepts separate in both code and language.

## Decision Broker

The runtime should own a small decision broker for pending proposal approvals and transport-level confirmations.

The broker's job is:

- create a durable request id for a pending decision
- expose bounded user choices
- wait for an explicit resolution or timeout
- report the final choice back to the caller

The broker should be transport-neutral. Telegram, CLI, or other surfaces may render the decision differently, but the pending state should not be embedded directly in `exec` or in ad hoc Telegram code.

The first concrete decision kinds should be:

- interrupt while busy
- stop-word confirmation while busy
- operational proposal approval

The broker is transport machinery, not the semantic source of the proposal.

The proposal itself should live in durable session state and be renderable independent of Telegram or CLI transport.

The same proposal discipline should apply to durable-child creation.

Examples:

- proposing the use of a channel adapter such as `child_adapter`
- proposing child-scoped credential binding
- proposing activation of a newly chartered durable child

Those are not ordinary shell-command approvals.
They are bounded operational proposals inside the admin conversation.

## Tool Authority Lifecycle (v1)

Tool capability rollout is a separate lifecycle from generic operation proposals.

The canonical chain is:

1. `capability_request` with `kind=tool` (tool should exist or be granted)
2. `review` through `capability_authority` (parent/admin decision)
3. `install`
4. `audit_run`
5. `probe_run` plus `install_set status=verified`
6. `registration` (known runtime tool definition)
7. `grant` (principal-level `invoke` allow)
8. `invocation` (checked against current principal, grant, freshness, and policy)

Normative requirements:

- `capability_request request_submit` creates capability requests in `proposed` state only.
- parent/admin approval and rejection go through `capability_authority request_review`.
- `capability_authority grant_set` is the only tool access grant writer.
- `register` must bind to a known trusted runtime tool definition; free-form `tool_name` strings are not sufficient.
- `implementation_ref` is metadata only and must not be treated as executable proof.
- effective access is decided at invocation time from current principal state plus active `capability_grants`; stale or expired grants do not allow access.

Status/readability requirements:

- status projections should distinguish capability request state (`proposed/parent_approved/approved/rejected`) from registration (`registered=true/false`) and grant status (`active/stale/revoked/expired`).
- tool authority status should be projected from canonical execution events with explicit source attribution.

## Durable-Agent Governance Tooling

The `durable_agent` governance surface should not be limited to editing already-existing children forever.

It should support, at minimum:

- listing registered children
- showing current policy
- creating a new draft child
- updating or ratifying a draft child's charter
- testing a channel connection
- activating a child once its charter and connection are ready
- guided setup wizard actions (`wizard_start`, `wizard_answer`, `wizard_show`, `wizard_finalize`, `wizard_cancel`) so conversational setup can persist as explicit machine state
- durable parent-child conversation actions (`conversation_send`, `conversation_show`) so parent guidance and child acknowledgements can persist as explicit threaded state

Creation and activation remain admin-only, but they should be reachable from ordinary conversation rather than only from startup config.

Wizard state should be durable, resumable, and explicit:

- persisted outside ephemeral turn context (for example in durable-agent state JSON)
- represented as a finite state machine with explicit `status`, `current_step`, and `missing` fields
- replayable after restart so setup can continue without re-asking answered questions

For `policy_apply`, the governance surface should prefer a conversational patch shape:

- `policy_patch` for charter/autonomy/visibility/shared-context/capabilities/drift
- `policy_overrides` only when low-level policy axes must be set explicitly

Policy changes are accepted through `policy_patch` and `policy_overrides`; removed top-level policy fields are rejected by the live schema.

## `TOOLS.md`

`TOOLS.md` is a valid Aphelion concept.

Unlike Hermes, which largely treats tool behavior as built-in instruction, Aphelion needs an operator-editable tool policy surface because tool behavior depends on:

- admin vs `approved_user`
- isolated vs global roots
- hidden paths
- network policy
- sandbox profile
- which tools are intentionally exposed on a given host

`TOOLS.md` therefore exists to tell the model things like:

- when to prefer inspection before mutation
- when non-admin work must stay inside isolated roots
- when to ask the admin session to take action in the global workspace
- local expectations for shell usage, patch hygiene, or risky commands

`TOOLS.md` must **not**:

- create new tools
- expand permissions
- bypass sandbox policy
- override config or code-level limits

This is closer to OpenClaw than Hermes: `TOOLS.md` is a local note surface, not the thing that makes tools real.

## Prompt Assembly

Per turn, tool guidance is assembled in this order:

1. machine-generated manifest from the active registry
2. role-specific execution constraints
3. optional workspace `TOOLS.md`

The model should never be shown stale tool definitions copied by hand into prompt text when the real registry differs.

Runtime discipline blocks (planning/operations/confirmation) should be keyed from typed registry capabilities (for example `exec`, `update_plan`, `update_operation`) rather than substring matches against free-form manifest text.

Tool guidance belongs to the **governor prompt**, not the face prompt.

This tool section is also a major part of the governor's runtime self-awareness.

## User-Facing Tool Progress

Tool progress is a transport/UI concern derived from real tool activity.

The runtime may rewrite raw tool starts into bounded semantic status such as:

- `Inspecting files`
- `Writing memory files`
- `Updating config`
- `Restarting service`

This rewriting must remain:

- machine-derived
- truthful to actual tool starts
- lower fidelity than the audit log
- configurable, with a raw trace mode available when needed

Raw tool payloads should remain in logs and durable records. Telegram progress should default to a human-readable phase view.

## Per-Run Manifest Shaping

The active tool manifest should be resolved from:

- principal role
- session kind
- run kind
- sandbox profile
- provider/runtime constraints

At minimum, these run kinds should be distinguished:

- `default`
- `heartbeat`
- `cron`
- `curiosity`
- `subagent`

This is the major place where Aphelion should follow OpenClaw more than Hermes.

Examples:

- `heartbeat` may omit ordinary user-facing messaging tools
- `cron` should usually get a lighter, narrower tool surface
- `curiosity` may receive read/retrieval tools only, and runtime must further bind execution to the preselected candidate source and exact input
- `curiosity` should execute through a non-admin approved-user read principal, not ambient admin reach; URL fetches keep the non-admin private/special-IP rejection path and record untrusted-source provenance
- `subagent` runs should inherit stricter ceilings than their parent
- `approved_user` runs should not merely receive warnings; they should receive a different actual manifest

The manifest should make those differences legible to the governor as runtime facts.
The runtime must enforce the effective run-kind manifest, not only describe it.
A tool that is absent from the manifest for a heartbeat, cron, recovery,
durable, or other non-interactive lane should not execute through that lane.

## Core Interfaces

The tool subsystem should stay narrow:

```go
type Registry interface {
    Definitions(principal Principal, scope ExecutionScope) []agent.ToolDef
    Execute(ctx context.Context, req *Request) (*Result, error)
}

type Request struct {
    Principal Principal
    Session   session.Session
    Name      string
    Input     json.RawMessage
}

type Result struct {
    Content      string
    Error        bool
    Truncated    bool
    StartedAt    time.Time
    FinishedAt   time.Time
    Audit        map[string]string
}
```

The current repo has a smaller interface. This spec describes the target shape.

An eventual implementation may also pass explicit `RunKind` and `SessionKind` fields rather than inferring them indirectly.

## Core Tool Surface

### `exec`

`exec` is the trusted-admin shell tool.

Input:

```json
{
  "command": "git status",
  "workdir": "."
}
```

Behavior:

- runs via `bash -lc`
- resolves `workdir` under the allowed root
- enforces timeout
- captures combined stdout/stderr
- truncates output to a configured byte budget
- returns non-zero exit as a tool error with captured output

For admin use, `exec` may target the configured admin workspace. Non-admin and
durable execution must go through scoped roots and sandbox readiness checks.

For clarity: root restriction alone is not sufficient for non-admin use. A
process can still reference other host paths or host networking unless the
sandbox layer enforces the configured profile.

### `exec` confirmation policy

`exec` should distinguish ordinary shell work from actions that deserve an explicit confirmation barrier.

The first implementation should stay simple:

- ordinary inspection and bounded development commands run directly
- clearly destructive or irreversible commands require explicit approval
- if no interactive approval surface exists, risky commands fail closed instead of silently running

This should be implemented as a guard in the `exec` enforcement path, not as a prompt-only suggestion.

Hermes-style global command approval patterns are a useful reference, but Aphelion should stay more scope-aware:

- the same command may mean different things under admin and isolated-user roots
- approval is about risky execution inside current authority
- out-of-authority actions belong to denial or escalation, not same-user approval

## Role-Aware Execution

### Admin

- tools may target the global workspace
- profile may be more permissive
- time, output, and resource bounds still apply

### `approved_user`

- by default, no tools should be exposed until the isolation floor is enforced
- tool execution starts inside the isolated per-user execution root
- writable roots are limited to that user’s isolated workspace and isolated memory
- global persona/shared memory are mounted or exposed read-only
- hidden paths are inaccessible
- network policy follows the configured non-admin sandbox profile

The difference between admin and non-admin must be enforced in code, not merely described in `TOOLS.md`.

## Run-Kind Policy

### Default turns

- normal interactive tool surface
- principal-aware filtering still applies

### Heartbeat turns

- governor-owned maintenance tool surface

### `semantic_search`

`semantic_search` is the memory-layer analogue of `session_search`.

It should:

- search a semantic index over approved curated memory corpora
- return bounded ranked hits
- remain governor-owned
- never mutate memory

It should not:

- silently inject itself into every turn
- outrank constitutional files
- replace `session_search` for transcript recall

Initial indexed sources should favor curated memory:

- `MEMORY.md`
- `memory/knowledge.md`
- `memory/decisions.md`
- optionally recent daily notes

Advanced practice files such as `memory/questions.md` and `memory/rhizome.md` may be searchable too, but their hits should remain clearly lower-authority.

Suggested result shape:

```json
{
  "query": "durable operator preference",
  "hits": [
    {
      "source": "memory/knowledge.md",
      "scope": "shared",
      "kind": "knowledge",
      "score": 0.91,
      "excerpt": "- Prefers concise progress updates [observed, confidence: 0.90]"
    }
  ]
}
```

The governor should receive these as explicit retrieved context, not as blended prompt truth.
- may be narrower than ordinary interactive turns
- should not silently inherit tools that only make sense for live user interaction

### Cron turns

- scheduled-job tool surface
- lightweight and explicit by default
- should not encourage improvised timers, sleep loops, or broad inherited context

### Subagent turns

- inherited principal ceiling
- explicit deny list for control-plane or escalation tools
- narrower than parent by default unless explicitly configured otherwise

## Execution Roots

Tool execution resolves against explicit roots:

- `global_root`
- `shared_memory_root`
- `user_workspace_root/<principal>`
- `user_memory_root/<principal>`

No tool may operate on an unresolved host path directly.

## Sandbox

The sandbox profile belongs to the execution layer, not the tool definition itself.

Required controls for non-admin execution:

- user namespace support when enabled
- dropped Linux capabilities
- resource limits
- hidden-path enforcement
- network policy: `none`, `firewall`, or `full`
- read-only vs read-write mount policy by root

Later hardening may use native Linux primitives directly or swap to a stronger backend such as `runsc`, but the observable contract to the rest of Aphelion should remain the same.

## Tool Families

### Required

- `exec`
- `read_file`
- `write_file`
- `list_dir`
- `search`
- `fetch_url`

The native file/search/fetch tools are more constrained and auditable than a
shell. They resolve paths through the current sandbox profile, reject
hidden/out-of-scope file paths, deny fetches when the profile network policy is
`deny`, and enforce isolated `allowlist` fetches against explicit destination
records.

Native read/fetch contracts:

- `read_file` must use a bounded window (`offset` + `limit`) or explicit
  `full=true`. Full reads remain capped by `max_bytes`.
- `fetch_url.max_bytes` controls the response bytes read and hashed.
- `fetch_url.excerpt_bytes` controls the visible excerpt returned to the model,
  defaults to a small bounded excerpt, and is capped by `max_bytes`.
- `fetch_url` returns a digest with status, content type, `bytes_read`,
  `sha256`, truncation status, excerpt size, and bounded excerpt. It must not
  claim an inaccessible raw-body reference.

### Later

- media helpers (`transcribe_audio`, `extract_pdf_text`)
- sub-agent launch/control helpers

### Implemented Optional External-Memory Tools

When configured, Aphelion may expose:

- `openai_file`
- `openai_vector_store`

These tools are:

- auxiliary to local truth
- admin-facing by default
- explicitly provider-backed rather than pretending to be local memory

Every new tool should justify its existence against the question: why is this better than a narrowly sandboxed `exec`?

Aphelion should generally prefer specialized tools when they provide one of:

- tighter sandboxability
- clearer audit shape
- lower prompt ambiguity
- less authority than shell access

### Implemented External Tool Manifests

When `[tools].external_manifest_dir` is configured, Aphelion loads external
tool manifests from JSON files in that directory.

External tools are authority-managed runtime tools:

- capability request approval and grants are separate from registration
- install, audit, and probe records are durable session state
- `install_set status=verified` requires current runtime-authored `audit_run`
  and `probe_run` evidence
- verified install and audit records store deterministic fingerprints covering
  manifest execution fields, IO schemas, constraints, install/probe commands,
  and local process/subprocess entry file contents
- registration, grant-gated listing, show, and invocation re-check the fingerprint
  and mark stale tools with an operator-readable reason
- stale external tools cannot be registered or invoked

The generic executor supports `process` and `subprocess` manifests through the
sandbox runner. `container` and `workspace_runner` manifests are importable and
diagnosable but are not process-executable until dedicated runtimes exist. They
are not current targets.

The bundled `browse_page` pilot lives under `external-tools/browse_page/`. It is
owned by `child-alpha`; invocation must be granted through
`capability_authority`. It uses a deterministic fixture implementation so
browser behavior remains outside core.

## Audit and Persistence

Every tool call should record:

- session id
- principal id
- tool name
- normalized input
- resolved execution root
- sandbox profile used
- start/end timestamps
- success/error/truncation

This data is part of the session ledger and may also feed review digests for non-admin sessions.

## Failure Model

Tool failure is normal and should be model-visible.

- validation errors return a tool error result
- sandbox denials return a tool error result
- timeout returns a tool error result
- transport/runtime crashes should still leave an audit trail where possible

The agent loop continues unless the turn budget or runtime policy says otherwise.

## Config Surface

Tool-related config lives primarily in `config.md` under:

- `[agent]`
- `[sandbox]`
- role-specific sandbox profiles
- output/time limits
- hidden paths
- network policy
- `network_allow` destinations for isolated allowlists

`TOOLS.md` is workspace data, not config.

## Test Plan

### Registry and Prompt

- **TestRegistryDefinitionsMatchManifest**: machine-generated manifest reflects actual registered tools
- **TestToolsMDIsAdvisoryOnly**: changing `TOOLS.md` changes prompt text but not actual tool availability
- **TestRoleSpecificDefinitions**: admin and non-admin see different effective tool surfaces when configured
- **TestRunKindSpecificDefinitions**: `default`, `heartbeat`, `cron`, and `subagent` runs can receive different effective manifests
- **TestFacePromptOmitsTools**: tool manifest and tool policy stay in the governor layer, not the face layer

### `exec`

- **TestExecSimpleCommand**: command output is returned
- **TestExecRejectsWorkspaceEscape**: `../` or equivalent escape is denied
- **TestExecTimeout**: long-running command is terminated and reported as timeout
- **TestExecOutputTruncation**: oversized output is truncated and marked
- **TestExecExitCodeError**: non-zero exit returns an error result with output

### Non-Admin Isolation

- **TestApprovedUserExecUsesIsolatedRoot**: non-admin execution starts in the isolated root
- **TestApprovedUserCannotWriteGlobalRoot**: non-admin cannot mutate the global workspace
- **TestApprovedUserCannotWriteSharedMemory**: non-admin cannot mutate shared memory/persona files
- **TestApprovedUserCannotReadHiddenSecrets**: configured hidden paths are inaccessible
- **TestApprovedUserSandboxHasDroppedCaps**: capability set is reduced as configured
- **TestApprovedUserSandboxHasNamespaceIsolation**: namespace profile is applied when enabled
- **TestApprovedUserSandboxNetworkPolicy**: network behavior matches configured policy

### Audit

- **TestToolAuditRecord**: successful tool execution records audit metadata
- **TestToolAuditOnFailure**: denied or failed execution still records audit metadata
- **TestReviewDigestRedactsRawToolOutput**: non-admin digests summarize tool behavior without forwarding raw tool transcripts by default

## File Layout

```text
tool/
├── registry.go
├── exec.go
├── file.go
├── prompt_manifest.go
└── sandbox/
    ├── profile.go
    ├── rootfs.go
    └── exec_linux.go
```

This is a target layout, not a claim about the current tree.
