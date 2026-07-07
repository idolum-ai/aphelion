# Durable External Runtimes

_Status: architecture proposal; not implemented runtime behavior._

This document describes how Aphelion can promote an existing thread into a
durable child whose executor is not another in-process Aphelion turn, but a
supervised Hermes or OpenClaw runtime. The design keeps the current durable
child security model: Aphelion owns identity, policy, wake authority, evidence,
and review; the child runtime owns only its local process, state, and bounded
task execution.

Creating a durable child should also be able to mean signing a bounded standing
work order up front. The admin can pre-authorize future work under explicit
conditions, such as schedule, audience, credential scope, runtime, domain,
endpoint, duration, and review policy. At execution time, Aphelion still
materializes narrow, consumable leases for the matching wake or action; the
child never receives ambient permission merely because the work was described
at creation.

The implementation order is generic first. Hermes and OpenClaw should be
adapter profiles over a common child-runtime contract, not bespoke parent-core
special cases.

## Reference Set

The analysis behind this proposal was grounded in these pinned revisions:

| System | Pinned revision | Relevant surfaces |
| --- | --- | --- |
| Aphelion | `873a264f664e95e5eeb721003007b9d2654b0cf8` | [`core.DurableAgent`](https://github.com/idolum-ai/aphelion/blob/873a264f664e95e5eeb721003007b9d2654b0cf8/core/durable_agents.go), [`core.ChildRuntimeContract`](https://github.com/idolum-ai/aphelion/blob/873a264f664e95e5eeb721003007b9d2654b0cf8/core/child_runtime.go), [`runtime/durable_wake.go`](https://github.com/idolum-ai/aphelion/blob/873a264f664e95e5eeb721003007b9d2654b0cf8/runtime/durable_wake.go), [`durableagent/`](https://github.com/idolum-ai/aphelion/tree/873a264f664e95e5eeb721003007b9d2654b0cf8/durableagent), [`telegram-child-bot`](https://github.com/idolum-ai/aphelion/blob/873a264f664e95e5eeb721003007b9d2654b0cf8/docs/architecture/telegram-child-bot-runbook.md) |
| Hermes Agent | `830165473e0920c2baf8c2a6863976edb0c52943` | [`pyproject.toml`](https://github.com/NousResearch/hermes-agent/blob/830165473e0920c2baf8c2a6863976edb0c52943/pyproject.toml), [`hermes_cli/main.py`](https://github.com/NousResearch/hermes-agent/blob/830165473e0920c2baf8c2a6863976edb0c52943/hermes_cli/main.py), [`acp_adapter/entry.py`](https://github.com/NousResearch/hermes-agent/blob/830165473e0920c2baf8c2a6863976edb0c52943/acp_adapter/entry.py), [`gateway/`](https://github.com/NousResearch/hermes-agent/tree/830165473e0920c2baf8c2a6863976edb0c52943/gateway) |
| OpenClaw | `c7295e417d5daec76c18fb452d117f7b8eadc4d6` | [`openclaw.mjs`](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/openclaw.mjs), [`docs/openclaw-agent-runtime.md`](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/docs/openclaw-agent-runtime.md), [`docs/cli/acp.md`](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/docs/cli/acp.md), [`docs/tools/acp-agents-setup.md`](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/docs/tools/acp-agents-setup.md) |

Local clone paths used for inspection:

- Hermes: `/tmp/hermes-agent`
- OpenClaw: `/tmp/openclaw-repo`

## Core Shape

Aphelion already has the durable-child ingredients needed for this direction:

- A durable child identity and policy record in `core.DurableAgent`.
- Child-local bootstrap, policy ceiling, storage roots, secret scopes, and
  network posture.
- Standing work orders, conditional grants, and schedule/trigger evaluation for
  future child work.
- Wake leases and continuation recovery around `durable_agent.wake_once`.
- External-channel runtime state for adapter status, cursor/session reference,
  failures, backoff, last artifact, and opaque adapter state.
- Review artifacts and parent conversation acknowledgement as the upward and
  downward collaboration paths.
- Child runtime materialization through `core.ChildRuntimeContract`, which can
  describe executable paths, read-only binds, secret binds, and parent-provided
  environment variables.

The missing layer is a first-class _runtime adapter contract_ between Aphelion's
durable child substrate and a non-Aphelion executor.

The parent should not learn Hermes or OpenClaw internals. It should know only
that a child runtime has:

- a source identity,
- an install and state root,
- lifecycle checks,
- a wake operation,
- bounded output and artifact contracts,
- and an authority ceiling enforced by Aphelion before the runtime starts.

A runtime source reference is descriptive until the runtime is attested. A child
does not become wake-eligible merely because a git ref, binary path, or image
name exists in the spec.

![Durable external runtimes](diagrams/07-durable-external-runtimes.svg)

## Authority Boundary

The central invariant is:

> Hermes and OpenClaw may produce child-local work and bounded projections.
> They may not grant themselves authority, mutate parent identity, or commit
> parent state without Aphelion accepting typed evidence through the usual
> durable-child lanes.

This keeps the existing durable-child model intact:

- Thread promotion is an Aphelion action.
- `child_wake` is still a separate, consumable authority event.
- Capability expansion still goes through capability requests, reviews, grants,
  attestation, and invocation checks.
- Child outputs remain projections until parent runtime records typed status,
  blocker class, retryability, artifact references, or review events.
- Channel tokens and gateway credentials are child-scoped runtime material, not
  proof of authority.

The Telegram child-bot runner is the closest current precedent: it gives one
durable child a narrow Telegram bot process, but token presence is not
authority. Hermes and OpenClaw should generalize that shape to richer runtimes,
not weaken it.

### Process Environment Boundary

Runtime adapters MUST launch external child runtimes with a sterile, allowlisted
environment. Parent process environment, parent `HOME`, parent working
directory, repo-local `.env` files, login-shell profiles, provider credentials,
channel credentials, and operator tokens are not inherited by default.

Every adapter invocation MUST set:

- an explicit child-local home/state/config root;
- an explicit working directory;
- an explicit environment allowlist derived from the normalized runtime spec and
  active child secret scopes;
- and no provider, channel, GitHub, Telegram, or shell credential variables
  unless an active child-scoped grant names them.

Adapter preflight must fail closed when the runtime would resolve implicit
parent state, implicit parent config, repo-local `.env` files, inherited
sensitive environment variables, or parent-owned credential stores.

### Install, Probe, And Drift Boundary

Durable external runtimes should reuse the external-tool lifecycle shape even
when they are not exposed as ordinary tools:

`request -> provision/install -> audit -> probe -> verify -> register -> wake/start -> observe -> renew/revoke`

Wake eligibility requires install/provision evidence, adapter audit, adapter
probe, executable/source fingerprinting, dependency/runtime baseline capture,
and a verified status whose anchors match the normalized runtime spec hash. Any
source, executable, dependency, entrypoint, config, or service drift fails
closed before adapter invocation.

This keeps runtime installation from becoming a weaker parallel path beside
the existing external-tool install/probe/drift vocabulary.

### License And External Terms Boundary

The intended integration target is self-hosted execution of upstream
MIT-licensed Hermes/OpenClaw runtime code. A runtime adapter grant is not a
license grant, a trademark grant, a hosted-service grant, or permission to
bypass third-party platform terms.

Adapter implementations MUST preserve the upstream copyright, license, and
third-party notice material when they copy, vendor, package, distribute, or
install substantial runtime material:

- Hermes Agent: pinned source plus upstream
  [MIT license](https://github.com/NousResearch/hermes-agent/blob/830165473e0920c2baf8c2a6863976edb0c52943/LICENSE).
- OpenClaw: pinned source plus upstream
  [MIT license](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/LICENSE)
  and
  [third-party notices](https://github.com/openclaw/openclaw/blob/c7295e417d5daec76c18fb452d117f7b8eadc4d6/THIRD_PARTY_NOTICES.md).

External services remain separate authority boundaries. WhatsApp, Telegram,
Discord, Slack, Signal, model providers, hosted gateways, OAuth subscriptions,
and API accounts are governed by their own terms and must enter the child only
through explicit child-scoped credentials and grants. The adapter must not
inherit parent provider credentials, channel tokens, hosted-service sessions,
or operator API keys merely because the upstream runtime can use them.

Aphelion-facing docs and UI should describe these as Aphelion adapter profiles
for self-hosted Hermes/OpenClaw runtimes unless an upstream relationship
explicitly authorizes stronger wording. Runtime names identify compatibility
targets; they are not evidence of endorsement.

### Standing Work Orders And Conditional Grants

Durable child creation may define future work, but executable authority is still
materialized only when the schedule, trigger, policy, identity, credential, and
review conditions match.

Use these layers:

| Layer | Meaning |
| --- | --- |
| Policy ceiling | The maximum capability envelope the child can ever request or receive. |
| Standing work order | Admin-signed statement of expected future work and conditions. |
| Conditional grant | Reusable grant template bound to schedule, trigger, identity, tool/channel, limits, and review policy. |
| Runtime lease | Short-lived, consumable authority generated for one wake/action after conditions match. |
| Exception approval | Moment-to-moment approval for unplanned work outside the standing work order. |

A standing work order is an operating mandate, not a daemon privilege. It can
authorize future leases, but it must not directly expose provider credentials,
channel tokens, browser sessions, webhooks, or public-send rights to the child.

Examples:

- Daily audience update: every morning, read email through `gog` using a
  child-scoped Gmail credential, summarize relevant messages, and draft a
  WhatsApp update through Hermes/OpenClaw. Sending remains `review_first`
  unless the standing work order names an autonomous-send audience and policy.
- Friday site watch: on Friday afternoon, open a browser session for up to six
  hours against named domains, synthesize changes, and call an allowlisted IFTTT
  webhook only through an exact endpoint/method/payload lease.

Standing work orders and conditional grants must be independently revocable and
auditable. A revoked work order stops future lease materialization; it does not
need to edit the runtime spec or erase child memory. A child request outside the
work order becomes an exception approval or a proposed work-order amendment.

## Runtime Modes

### Oneshot Wake Mode

This is the recommended first implementation target.

The parent records a child wake claim, materializes any matching standing-work
order leases, builds a bounded task packet, invokes the runtime adapter once,
collects a typed result, and only then acknowledges parent conversation
messages. The runtime may use its local memory and tools, but it does not keep
an ambient public gateway open.

Use this mode for:

- promoted work threads;
- scheduled child work;
- local draft/review children;
- first Hermes/OpenClaw adapter support;
- CI and live smoke tests.

### Gateway Presence Mode

This is a later mode for children that need their own Telegram, WhatsApp, or
other channel presence through Hermes/OpenClaw gateways.

Starting a gateway is a larger authority crossing than a oneshot wake. It gives
the child a standing external ingress and possibly outbound delivery. It should
require an explicit `gateway_presence` grant that names:

- runtime kind,
- channel or account,
- allowed inbound mode,
- outbound mode,
- token/secret scope,
- state root,
- review target,
- and stop/revoke behavior.

The gateway may receive messages directly, but Aphelion still owns whether those
messages become durable parent-observed events, review artifacts, or accepted
child outputs.

### Remote Service Mode

`remote_service` is a reserved mode for a child runtime hosted outside the
parent process host. It must fail closed until the implementation defines:

- mutually authenticated parent/child control traffic;
- parent-issued task packets;
- result signing or equivalent integrity evidence;
- bounded artifact fetch;
- explicit revocation and stop semantics;
- and no parent-message acknowledgement outside accepted typed results.

## User Flows

### Promote A Thread To A Hermes Or OpenClaw Child

1. Operator starts or continues a normal Aphelion thread.
2. The thread becomes important enough to retain, isolate, wake, or bind.
3. Operator chooses `Promote` from the thread surface.
4. Aphelion creates or updates a durable-agent record with `runtime.kind`
   proposed as `aphelion`, `hermes`, or `openclaw`.
5. The promotion wizard summarizes inherited context, proposed charter, storage
   roots, runtime source, authority ceiling, and any standing work orders.
6. Admin approval activates the durable child record and signed standing work
   orders but does not automatically run it.
7. A later `wake_once` consumes a `child_wake` lease, materializes matching
   conditional grants into narrow runtime leases, and invokes the configured
   runtime adapter.
8. The adapter writes a child task result and artifact references.
9. Aphelion records the result as typed durable evidence before advancing parent
   plans or acknowledging parent conversation messages.

### Define A Standing Work Order

1. Admin defines the durable child role, charter, policy ceiling, runtime kind,
   state roots, secret scopes, schedules, and review target.
2. Admin adds one or more conditional grants for concrete future actions such as
   `gog` email read, WhatsApp draft, browser monitoring, or webhook alert.
3. Aphelion validates that each grant names a credential scope, network class,
   allowed action, selectors, limits, review policy, and revocation behavior.
4. The standing work order is stored as inactive until approved.
5. Approval activates the work order but exposes no live credential or tool
   authority to the child.
6. When a schedule or trigger fires, Aphelion evaluates the active work order,
   runtime status, credential status, drift anchors, and policy constraints.
7. Matching grants become short-lived runtime leases in the `ChildTaskPacket`.
   Non-matching, expired, revoked, or drifted grants produce typed blockers.
8. The child receives only the leases for the current wake/action. Any request
   outside those leases becomes an exception approval or amendment proposal.

### Install Or Repair A Runtime

1. Status/preflight finds missing runtime material, stale source revision, absent
   dependency, or invalid state/config root.
2. Runtime adapter returns a typed blocker such as `runtime_missing`,
   `preflight_failed`, `state_root_invalid`, or `credentials_missing`.
3. Aphelion records the blocker in durable runtime state and queues bounded
   operator review when needed.
4. Operator grants or performs install/repair through the existing capability
   and tool lifecycle lanes.
5. Adapter preflight passes and future wakes may run.

### Give A Child Public Channel Presence

1. A durable child requests gateway presence for a concrete surface such as a
   WhatsApp account or Telegram bot token.
2. Aphelion records a capability request with proposed runtime, channel,
   credential scope, and outbound policy.
3. Admin approves and grants the capability after credential provisioning and
   runtime preflight.
4. Adapter starts the Hermes/OpenClaw gateway as a child service with child-local
   state and secrets only.
5. Incoming channel events are handled through the child runtime and reported
   upward as bounded results, review artifacts, or typed blockers.
6. Revocation stops the gateway, marks authority inactive, and prevents future
   restarts until a fresh grant exists.

## Network Classes

Runtime specs and task packets should not use a single broad network switch.
Hermes and OpenClaw may need model-provider egress without needing public web or
channel presence. The parent should name the smallest active network class:

| Class | Meaning | Authority source |
| --- | --- | --- |
| `none` | No network. | Runtime/task default. |
| `provider_egress` | Model/provider calls using child-scoped credentials. | Child bootstrap or conditional grant. |
| `runtime_control_local` | Loopback socket/WebSocket to a child-owned local gateway. | Runtime spec plus verified adapter status. |
| `public_channel_presence` | Standing external ingress and outbound channel delivery. | Explicit `gateway_presence` grant. |
| `webhook_egress` | Exact approved outbound webhook calls. | Conditional grant with endpoint/method/payload constraints. |
| `public_web` | Arbitrary web fetch/browse/search behavior. | Separate public-web/tool capability. |

Provider egress, local runtime control, public channel presence, webhook egress,
and public web must remain independently grantable and independently revocable.

## Proposed Schemas

These schemas are documented contracts, not implemented storage fields in the
current codebase.

### DurableAgentRuntimeSpec

```json
{
  "runtime": {
    "kind": "hermes",
    "mode": "oneshot",
    "source": {
      "kind": "git",
      "repo": "https://github.com/NousResearch/hermes-agent",
      "ref": "830165473e0920c2baf8c2a6863976edb0c52943"
    },
    "install_root": "/var/lib/aphelion/children/research/hermes-agent",
    "state_root": "/var/lib/aphelion/children/research/hermes",
    "workspace_root": "/var/lib/aphelion/children/research/workspace",
    "entrypoint": {
      "kind": "acp_stdio",
      "command": ["hermes-acp"]
    },
    "env": {
      "HERMES_HOME": "/var/lib/aphelion/children/research/hermes"
    }
  }
}
```

Required fields:

- `kind`: `aphelion`, `hermes`, `openclaw`, or future adapter ID.
- `mode`: `oneshot`, `gateway_presence`, or `remote_service`.
- `source`: immutable or operator-owned runtime source reference.
- `state_root`: child-owned persistent state root.
- `workspace_root`: child-owned workspace root when the runtime can perform
  workspace work.
- `entrypoint`: adapter-resolved command or protocol mode.

### RuntimeSourceRef

```json
{
  "kind": "git",
  "repo": "https://github.com/openclaw/openclaw",
  "ref": "c7295e417d5daec76c18fb452d117f7b8eadc4d6",
  "integrity": {
    "commit": "c7295e417d5daec76c18fb452d117f7b8eadc4d6"
  }
}
```

Allowed `kind` values should start narrow:

- `git`: local clone or fetched checkout pinned by commit.
- `binary`: operator-provisioned executable with fingerprint.
- `container_image`: image reference plus digest.

### StandingWorkOrder

```json
{
  "standing_work_order": {
    "id": "swo_daily_audience_update",
    "agent_id": "audience-child",
    "status": "active",
    "title": "Daily email review and WhatsApp draft",
    "runtime_kind": "openclaw",
    "policy_ceiling_ref": "durable_agent:audience-child:policy",
    "schedule": {
      "kind": "cron",
      "expression": "0 13 * * *",
      "timezone": "America/New_York"
    },
    "review_policy": {
      "default_outbound": "draft_only",
      "send_requires": "parent_review"
    },
    "conditional_grant_ids": [
      "grant_gmail_read",
      "grant_whatsapp_draft"
    ],
    "revocation": {
      "stop_future_leases": true,
      "stop_running_gateway": true
    }
  }
}
```

### ConditionalGrant

```json
{
  "conditional_grant": {
    "id": "grant_gmail_read",
    "standing_work_order_id": "swo_daily_audience_update",
    "capability": "gmail_read",
    "tool": "gog",
    "actions": ["gmail.search", "gmail.read"],
    "credential_scope": "secret_scope:child:audience-child:gmail-read",
    "conditions": {
      "triggers": ["schedule:swo_daily_audience_update"],
      "max_messages": 50
    },
    "constraints": {
      "accounts": ["updates@example.com"],
      "forbidden_actions": ["gmail.send", "gmail.delete", "gmail.modify_labels"]
    },
    "materializes": {
      "lease_kind": "tool_invocation",
      "ttl_seconds": 900
    }
  }
}
```

```json
{
  "conditional_grant": {
    "id": "grant_friday_site_watch_ifttt",
    "standing_work_order_id": "swo_friday_site_watch",
    "capability": "web_monitor_and_alert",
    "actions": ["browser.monitor", "webhook.call"],
    "credential_scope": "secret_scope:child:watch-child:ifttt",
    "conditions": {
      "schedule": {
        "kind": "cron",
        "expression": "0 18 * * 5",
        "timezone": "America/New_York"
      },
      "max_duration_seconds": 21600
    },
    "constraints": {
      "allowed_domains": ["example.com"],
      "webhook": {
        "method": "POST",
        "endpoint": "https://maker.ifttt.com/trigger/site_changed/with/key/<child-secret-ref>",
        "payload_schema_ref": "schema:site-change-alert-v1"
      }
    },
    "materializes": {
      "lease_kind": "runtime_task",
      "ttl_seconds": 21600
    }
  }
}
```

Conditional grants are templates. They become executable only when Aphelion
evaluates the standing work order and emits runtime leases for the current wake
or action.

### LeaseMaterialization

```json
{
  "lease_materialization": {
    "id": "lm_01J...",
    "agent_id": "audience-child",
    "standing_work_order_id": "swo_daily_audience_update",
    "matched_conditions": {
      "trigger": "schedule",
      "schedule_tick": "2026-07-07T13:00:00-04:00"
    },
    "runtime_spec_hash": "sha256:...",
    "issued_leases": [
      {
        "lease_id": "lease_gmail_read_01J...",
        "conditional_grant_id": "grant_gmail_read",
        "capability": "gmail_read",
        "expires_at": "2026-07-07T13:15:00-04:00"
      },
      {
        "lease_id": "lease_whatsapp_draft_01J...",
        "conditional_grant_id": "grant_whatsapp_draft",
        "capability": "channel_draft",
        "expires_at": "2026-07-07T13:15:00-04:00"
      }
    ]
  }
}
```

### ChildRuntimeAdapter Operations

```json
{
  "operation": "wake",
  "agent_id": "research-child",
  "runtime_kind": "openclaw",
  "runtime_mode": "oneshot",
  "spec_hash": "sha256:...",
  "input_ref": "artifact:child-task-packet/...",
  "authority_ref": "continuation_lease:...",
  "deadline": "2026-07-06T19:45:00Z"
}
```

Minimum operations:

- `preflight`: validate executable/config/state/secrets without running a turn.
- `install_status`: report installed source and drift.
- `start`: start a long-running gateway or remote-service mode.
- `stop`: stop long-running runtime processes.
- `status`: report process and adapter health.
- `wake`: run one bounded child task.
- `collect_artifacts`: return bounded artifacts after a wake or gateway event.

### ChildTaskPacket

```json
{
  "schema": "aphelion.child_task_packet.v1",
  "agent_id": "research-child",
  "parent_agent_id": "house",
  "wake_id": "wake_01J...",
  "session_id": "agent:research-child:wake:...",
  "charter": "Review the repository and report blockers.",
  "guidance": [
    {
      "id": "parent_msg_123",
      "text": "Check whether the release notes capture the durable runtime value."
    }
  ],
  "authority": {
    "wake_lease_id": "lease_01J...",
    "standing_work_order_id": "swo_daily_audience_update",
    "lease_materialization_id": "lm_01J...",
    "materialized_leases": [
      {
        "lease_id": "lease_gmail_read_01J...",
        "conditional_grant_id": "grant_gmail_read",
        "capability": "gmail_read"
      },
      {
        "lease_id": "lease_whatsapp_draft_01J...",
        "conditional_grant_id": "grant_whatsapp_draft",
        "capability": "channel_draft"
      }
    ],
    "exception_approval_refs": []
  },
  "limits": {
    "deadline": "2026-07-06T19:45:00Z",
    "max_output_bytes": 65536,
    "network_classes": ["provider_egress", "public_channel_presence"]
  },
  "expected_result": {
    "kind": "review_artifact",
    "acknowledge_parent_message_ids": ["parent_msg_123"]
  }
}
```

### ChildTaskResult

```json
{
  "schema": "aphelion.child_task_result.v1",
  "agent_id": "research-child",
  "wake_id": "wake_01J...",
  "task_packet_id": "packet_01J...",
  "attempt_id": "attempt_01J...",
  "lease_generation": "lease_generation_3",
  "input_packet_hash": "sha256:...",
  "runtime_spec_hash": "sha256:...",
  "adapter_run_id": "adapter_run_01J...",
  "status": "completed",
  "started_at": "2026-07-06T19:40:00Z",
  "completed_at": "2026-07-06T19:42:11Z",
  "summary": "The release notes miss the external runtime roadmap.",
  "acknowledged_parent_message_ids": ["parent_msg_123"],
  "artifacts": [
    {
      "kind": "review",
      "ref": "artifact:durable-child/research-child/review-01J..."
    }
  ],
  "blocker": null,
  "exit_code": 0,
  "protocol_completion": "completed",
  "artifact_manifest_hash": "sha256:...",
  "truncated": false,
  "stdout_ref": "artifact:durable-child/research-child/stdout-01J...",
  "stderr_ref": "artifact:durable-child/research-child/stderr-01J...",
  "runtime": {
    "kind": "openclaw",
    "source_ref": "c7295e417d5daec76c18fb452d117f7b8eadc4d6",
    "state_root": "/var/lib/aphelion/children/research/openclaw"
  }
}
```

`status` should be one of:

- `completed`
- `blocked`
- `failed`
- `timed_out`
- `cancelled`

Blocked/failed results must include typed `blocker` data so parent plans can
stop usefully instead of looping on prose.

Parent acknowledgement is parent-computed. A child result may claim consumed
parent message IDs, but Aphelion acknowledges only packet-provided IDs that are
accepted under the current task packet, attempt, lease generation or fencing
token, runtime spec hash, and committed result. Unknown IDs, stale IDs,
cross-child IDs, duplicate IDs, and late claims are recorded as projections and
do not acknowledge parent conversation messages.

### GatewayPresenceContract

```json
{
  "gateway_presence": {
    "runtime_kind": "hermes",
    "channel": "whatsapp",
    "account": "support-line",
    "standing_work_order_id": "swo_daily_audience_update",
    "conditional_grant_id": "grant_whatsapp_draft",
    "inbound_mode": "paired_contacts_only",
    "outbound_mode": "reply_with_parent_review",
    "allowed_sender_ids": ["+15551234567"],
    "pairing_policy": "required_for_unknown_senders",
    "unknown_sender_behavior": "pairing_only_no_memory",
    "memory_admission": "after_parent_review",
    "inbound_event_evidence": {
      "transport_message_id": "wamid.HBg...",
      "sender_id": "+15551234567",
      "channel_id": "whatsapp:support-line",
      "adapter_timestamp": "2026-07-06T19:30:00Z",
      "raw_payload_hash": "sha256:..."
    },
    "outbound_delivery_policy": "review_first",
    "credential_scope": "secret_scope:child:research-child:whatsapp",
    "state_root": "/var/lib/aphelion/children/research/hermes",
    "review_target_chat_id": 123456789,
    "stop_on_revoke": true
  }
}
```

This contract belongs in a capability request/grant path. It may be referenced
by a standing work order, but it should not be silently inferred from a runtime
kind, channel name, or schedule.

Gateway contracts must encode sender identity and memory admission explicitly.
Adapter-local pairing or allowlist defaults are defense-in-depth, not the
parent authority contract. Unknown senders must not become child-memory
contaminants unless the grant permits that memory admission path.

## Adapter Profiles

### Hermes

Hermes exposes:

- `hermes`: interactive CLI.
- `hermes gateway`: messaging gateway for Telegram, Discord, Slack, WhatsApp,
  Signal, and related surfaces.
- `hermes-acp`: ACP stdio adapter.
- `HERMES_HOME`: install/state convention used by the Hermes installer and
  managed checkout.

Preferred Aphelion mapping:

- Use `hermes-acp` or another non-interactive entrypoint for oneshot wake mode.
- Use `hermes gateway` only after a `gateway_presence` grant exists.
- Set `HERMES_HOME` to the child state root.
- Keep provider/channel credentials under child secret scopes.

### OpenClaw

OpenClaw exposes:

- `openclaw agent --message ...`: one agent turn through the gateway.
- `openclaw gateway`: long-running gateway.
- `openclaw acp`: ACP server that forwards prompts into gateway sessions.
- `OPENCLAW_STATE_DIR` and `OPENCLAW_CONFIG_PATH`: state/config isolation
  controls.
- Gateway channel support including WhatsApp, Telegram, Slack, Discord, Signal,
  Matrix, iMessage, and other adapters.

Preferred Aphelion mapping:

- Use an ephemeral child-local gateway for initial oneshot wake mode: start an
  isolated OpenClaw gateway with channels disabled, loopback-only binding,
  child-local state/config roots, and a short-lived local token; run one
  `openclaw agent --message ...` or `openclaw acp` turn; collect the result;
  then stop the gateway.
- Pass gateway tokens by child-scoped env or token files, not command-line
  arguments that can appear in process listings.
- Treat a long-lived child-local OpenClaw gateway as a separate supervised
  `runtime_control_local` service mode, not the default oneshot path.
- Set `OPENCLAW_STATE_DIR` and `OPENCLAW_CONFIG_PATH` to child-local paths.
- Treat OpenClaw pairing/allowlist mechanisms as child-side defense-in-depth,
  not as parent authority.
- Use `openclaw gateway` only under explicit gateway-presence grants.

## State And Truth Classes

| Question | Canonical source |
| --- | --- |
| Does the durable child exist? | Aphelion session durable-agent record |
| What runtime is configured? | Proposed Aphelion durable runtime spec |
| What future work is pre-authorized? | Active Aphelion standing work order |
| Which future actions may become leases? | Active conditional grants under that work order |
| May the child wake now? | Active Aphelion continuation lease and policy |
| What authority is in this task packet? | Lease materialization for the current wake/action |
| May the runtime start a public gateway? | Active Aphelion capability grant |
| What did the child claim happened? | Child task result projection |
| What did Aphelion accept as durable evidence? | TES/session evidence and review artifacts |
| What is the child runtime's local memory? | Child runtime state root |
| What secrets may the child see? | Child secret scopes and active grants |

The parent should project child runtime health in `/agents`, `/status`, and
health traces, but those projections must keep source attribution.

## Failure Modes

| Failure | Parent behavior |
| --- | --- |
| Runtime executable missing | Record `runtime_missing`, do not consume parent guidance as acknowledged. |
| Runtime source drift | Mark runtime status stale and require re-attestation or operator repair. |
| Standing work order condition does not match | Do not materialize leases; record skipped or blocked wake evidence. |
| Conditional grant revoked or expired | Block future lease materialization and surface the grant state. |
| Missing child state root | Block wake before invoking runtime. |
| Missing credential for gateway | Block gateway start; do not start degraded public presence. |
| Child result claims new authority | Treat as projection and convert to capability request if appropriate. |
| Child wake times out | Record typed timeout, keep unacknowledged parent messages pending. |
| Gateway receives unknown sender | Child-side adapter may pair/block; parent accepts only bounded artifacts. |
| Parent grant revoked while gateway runs | Stop runtime through adapter and mark future starts unauthorized. |

## Logical Implementation Roadmap

### 1. Standing Work Order Contract

Define standing work order normalization, validation, operator display, and
lease materialization before runtime-specific adapters. The contract should
describe the durable child, policy ceiling, schedules/triggers, conditional
grants, credential scopes, network classes, review policy, revocation behavior,
and the runtime leases emitted for a wake/action.

The runtime adapter should receive only materialized leases in the task packet,
not the full standing work order as authority.

### 2. Generic Runtime Contract

Define durable runtime spec normalization, validation, status projection, and
operator display without naming Hermes or OpenClaw in core logic. The contract
should be able to describe a local executable, source reference, child state
root, workspace root, entrypoint kind, mode, process environment allowlist, and
network classes.

### 3. Adapter Registry

Introduce a parent-side registry that maps `runtime.kind` to adapter
implementations. The registry should expose `preflight`, `status`, `wake`,
`start`, and `stop` operations. Unknown runtime kinds must fail closed with a
typed blocker that can become a capability/delegation request.

### 4. Oneshot Wake Executor

Add a wake path that evaluates active standing work orders, materializes
matching conditional grants into narrow leases, builds a `ChildTaskPacket`,
invokes the selected adapter once, records a `ChildTaskResult`, and
acknowledges parent conversation messages only when the result says they were
consumed.

### 5. Runtime Evidence And Recovery

Persist runtime status, source hash, last preflight, last wake, failure class,
backoff, and artifact refs under the existing durable child runtime-state
pattern. Reuse current loop-shedding and recovery-contract behavior so repeated
runtime failures stop with actionable blockers.

Runtime status must include verified install/probe anchors, dependency baseline,
entrypoint fingerprint, runtime spec hash, and stale reason when any anchor
drifts.

### 6. Hermes Adapter

Implement Hermes support over the generic adapter contract. Start with
non-interactive oneshot mode using child-local `HERMES_HOME`; add gateway mode
only after the generic gateway-presence contract exists.

### 7. OpenClaw Adapter

Implement OpenClaw support over the same contract. Start with gateway-backed
oneshot mode by launching an ephemeral loopback gateway per wake with channels
disabled and child-local `OPENCLAW_STATE_DIR`/`OPENCLAW_CONFIG_PATH`; add
long-lived gateway modes later under `runtime_control_local` or
`gateway_presence` contracts.

### 8. Gateway Presence

Add public or semi-public channel presence as a generic capability grant. The
implementation should not have `whatsapp`, `telegram`, `hermes`, or `openclaw`
authority shortcuts. Runtime adapters translate granted gateway contracts into
their own service commands.

### 9. Operator UX

Expose runtime kind, mode, standing work orders, conditional grants, last lease
materialization, status, last wake, blockers, and repair actions in thread
promotion, `/agents`, and health traces. Keep implementation details behind
diagnostics unless the operator is granting runtime installation, gateway
presence, secret scopes, or standing work-order authority.

### 10. Evaluation And Live Smoke

Add adapter contract tests with fake runtimes before live Hermes/OpenClaw tests.
Live smoke should validate installation/preflight and one bounded wake under a
temporary child state root, not public gateway delivery by default.

## Test Matrix

| Area | Required scenarios |
| --- | --- |
| Runtime spec | Valid Hermes/OpenClaw specs, missing roots, unknown kind, invalid source, env containment. |
| Standing work order | No lease before schedule/trigger match; matching schedule materializes only named grants; revoked or expired work order prevents future leases. |
| Conditional grants | Daily `gog` email read grants search/read only; WhatsApp draft does not imply send; autonomous send requires named audience and outbound policy. |
| Browser and webhook work | Browser monitoring is domain/time bounded; IFTTT calls require exact endpoint, method, payload schema, credential scope, and `webhook_egress`. |
| Wake authority | Missing `child_wake` lease blocks before adapter invocation; active lease invokes exactly once. |
| Process environment | Fake runtime sees explicit cwd/home/state roots and does not see parent `HOME`, cwd, `.env`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `TELEGRAM_BOT_TOKEN`, `GITHUB_TOKEN`, or shell profile env unless granted. |
| Install/probe/drift | Runtime is not wake-eligible until install/provision, audit, probe, verify, fingerprint, and dependency baseline anchors match the runtime spec hash; drift blocks before invocation. |
| License and external terms | Install/package paths preserve upstream license and notice material; provider/channel credentials require explicit child-scoped grants; docs/UI do not imply upstream endorsement. |
| Result acceptance | Projection cannot grant authority; accepted result records typed status, artifact hashes, fencing fields, and artifacts. |
| Parent conversation | Messages acknowledged only by parent-computed intersection after successful adapter consumption under current attempt/fencing token. |
| Runtime failures | Missing executable, timeout, stale source, failed preflight, repeated same blocker/backoff, late result after timeout, duplicate result after retry. |
| Gateway presence | Start requires explicit grant; revoked grant stops runtime and prevents restart; unknown senders follow grant-defined pairing/memory admission. |
| Secret isolation | Parent Telegram/GitHub/provider tokens are not inherited unless named in child scope. |
| Hermes profile | Child-local `HERMES_HOME`, pinned source, ACP/oneshot preflight, gateway gated. |
| OpenClaw profile | Ephemeral loopback gateway per oneshot wake, child-local `OPENCLAW_STATE_DIR` and `OPENCLAW_CONFIG_PATH`, token not exposed in argv, public channels disabled, long-lived gateway gated. |

## Design Constraints

- Do not add protocol-specific durable-agent core fields for every channel.
  Protocol residue belongs under adapter state.
- Do not use runtime kind as authority. `runtime_kind=openclaw` does not imply
  WhatsApp access, network access, file access, or public reply authority.
- Do not treat a standing work order as ambient runtime authority. It must
  compile into bounded leases at wake/action time.
- Do not collapse email read, browser monitoring, webhook calls, channel draft,
  and channel send into one broad external-access grant.
- Do not treat an upstream runtime license as permission to use third-party
  channels, hosted services, model providers, trademarks, or parent credentials.
- Do not let child gateways acknowledge parent conversation messages by mere
  process start. Acknowledgement belongs to the wake/result contract.
- Do not trust child-claimed acknowledgements directly. Parent acknowledgement
  is computed from accepted task/result evidence and current fencing data.
- Do not treat child runtime local memory as parent memory. Promotion into
  parent memory remains a separate governed event.
- Do not make the parent a Hermes/OpenClaw configuration editor. The parent
  should provision bounded runtime contracts and record evidence.
