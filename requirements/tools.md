# Tools

## Goal

Tools let the agent act on the host in bounded, inspectable ways.

Aphelion treats tools as a **system surface**, not just an LLM convenience:

- tool availability is defined in code
- tool behavior is enforced by code and config
- tool policy may be explained to the model via prompt text
- prompt text is never the source of truth for security

This spec is **staged**.

- **v0**: one real tool, `exec`, for the admin DM only
- **v0.5**: role-aware tools and sandboxed non-admin execution
- **later**: broader toolset (`read_file`, `write_file`, web/media helpers, OpenAI-backed storage/media helpers, sub-agents)

## Philosophy

1. **Reality in code.** The registry, schemas, sandbox, and permissions live in Go.
2. **Policy in prompt text.** `TOOLS.md` may explain style, risk posture, and preferred usage to the model.
3. **Least privilege.** Non-admin sessions only get tools and writable roots needed for their isolated scope.
4. **Audit first.** Every tool call is durable session data.
5. **One registry.** The agent sees a single normalized tool surface even if implementation backends differ.

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

## Prompt Assembly

Per turn, tool guidance is assembled in this order:

1. machine-generated manifest from the active registry
2. role-specific execution constraints
3. optional workspace `TOOLS.md`

The model should never be shown stale tool definitions copied by hand into prompt text when the real registry differs.

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

## v0 Tool Surface

### `exec`

`exec` is the only required v0 tool.

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

For v0, `exec` may target the configured admin workspace and remain simpler than the target sandbox architecture, but the interface and audit shape should leave room for role-aware sandboxing.

## v0.5 Role-Aware Execution

### Admin

- tools may target the global workspace
- profile may be more permissive
- time, output, and resource bounds still apply

### `approved_user`

- tool execution starts inside the isolated per-user execution root
- writable roots are limited to that user’s isolated workspace and isolated memory
- global persona/shared memory are mounted or exposed read-only
- hidden paths are inaccessible
- network policy follows the configured non-admin sandbox profile

The difference between admin and non-admin must be enforced in code, not merely described in `TOOLS.md`.

## Execution Roots

Tool execution resolves against explicit roots:

- `global_root`
- `shared_memory_root`
- `user_workspace_root/<principal>`
- `user_memory_root/<principal>`

No tool may operate on an unresolved host path directly.

## Sandbox

The sandbox profile belongs to the execution layer, not the tool definition itself.

Required controls for non-admin execution in v0.5:

- user namespace support when enabled
- dropped Linux capabilities
- resource limits
- hidden-path enforcement
- network policy: `none`, `firewall`, or `full`
- read-only vs read-write mount policy by root

Later hardening may use native Linux primitives directly or swap to a stronger backend such as `runsc`, but the observable contract to the rest of Aphelion should remain the same.

## Planned Tool Families

### Required

- `exec`

### Likely next

- `read_file`
- `write_file`
- `list_dir`
- `search`

These are useful because they can be more constrained and auditable than a shell.

### Later

- `fetch_url`
- media helpers (`transcribe_audio`, `extract_pdf_text`)
- OpenAI-backed file storage helpers
- retrieval/vector-store helpers
- sub-agent launch/control helpers

Every new tool should justify its existence against the question: why is this better than a narrowly sandboxed `exec`?

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

`TOOLS.md` is workspace data, not config.

## Test Plan

### Registry and Prompt

- **TestRegistryDefinitionsMatchManifest**: machine-generated manifest reflects actual registered tools
- **TestToolsMDIsAdvisoryOnly**: changing `TOOLS.md` changes prompt text but not actual tool availability
- **TestRoleSpecificDefinitions**: admin and non-admin see different effective tool surfaces when configured

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
