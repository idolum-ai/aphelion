# Synth Telegram Bot Subsystem Plan

Status: planning contract, not implementation.  
Date: 2026-05-05.  
Scope: a separate visible Telegram bot identity for durable child `synth`, while keeping governance in Aphelion.

## Objective

Create a Synth-specific Telegram bot front for Mada's job-search group without turning it into a second autonomous Aphelion.

Synth must be visibly Synth in Telegram, but all cognition, memory, tools, policy, and authority must remain governed by Aphelion's durable-agent runtime.

## Non-goals for the first implementation

- No mailbox reading, CV parsing, public job search, applications, recruiter contact, or email mutation.
- No group history import or silent transcript scrape.
- No reuse of Daniel/Idolum private chat context.
- No per-child hidden authority or self-issued capability grants.
- No replacement of the global Aphelion Telegram bot token.
- No broad multi-bot framework unless the narrow Synth runner forces a reusable seam.

## Current facts to preserve

- `synth` already exists as a durable child shell with `channel_kind=telegram_group`.
- Synth policy is `read_only`, `private`, `shared_context=isolated`.
- Synth profile/artifact roots are child-local under `state/durable_agents/synth/...`.
- Synth Gmail uses a named gog client/account pair: `client=synth`, `account=synth@idolum.ai`.
- The Synth bot token is staged at `~/.aphelion/secrets/synth/telegram-bot-token` and must not be printed.
- Current `[telegram] bot_token` is global; `[[telegram.durable_groups]]` has no per-group token field.

## Design invariant

The Synth Telegram process is a transport adapter only.

It may:

1. read the Synth bot token,
2. poll only the configured group,
3. normalize updates into `core.InboundMessage`,
4. set `DurableAgentID = "synth"`,
5. call the existing durable telegram child runner,
6. send only the runner-approved reply through the Synth bot client,
7. emit health/audit events.

It may not:

- load or call a model directly,
- bypass durable-agent policy/bootstrap/profile loading,
- grant itself capabilities,
- write parent/global memory directly,
- send outside configured chats,
- mutate config/policy at runtime,
- use email/web/CV tools unless separate grants exist.

## Runtime shape

Preferred v0 shape: a separate command/process sharing the same installed Aphelion binary.

Example command:

```bash
aphelion synth-telegram \
  --config ~/.aphelion/aphelion.toml \
  --agent synth \
  --token-file ~/.aphelion/secrets/synth/telegram-bot-token \
  --chat-id -5056905988 \
  --respond-on mentions
```

Likely systemd unit:

```ini
[Unit]
Description=Aphelion Synth Telegram bot
After=network-online.target aphelion.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/home/sadasant_gmail_com/code/github.com/idolum-ai/aphelion/bin/aphelion synth-telegram --config /home/sadasant_gmail_com/.aphelion/aphelion.toml --agent synth --token-file /home/sadasant_gmail_com/.aphelion/secrets/synth/telegram-bot-token --chat-id -5056905988 --respond-on mentions
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

The separate unit is deliberately narrow. It keeps the main Aphelion bot untouched and makes the visible Telegram identity Synth.

## Data flow

```text
Telegram update from -5056905988
  -> Synth bot poller using Synth token
  -> admission: chat_id allowlist + respond_on rule
  -> core.InboundMessage{DurableAgentID:"synth", ChatID:-5056905988, ChatType:"group|supergroup"}
  -> Runtime.RunDurableTelegramGroupChild / existing durable child executor
  -> child scope: durable_agents/synth workspace + memory/profile
  -> governor/continuation/capability gates
  -> DurableGroupChildResult{AllowLocalReply, ReplyText, Media}
  -> Synth bot client sends reply only when allowed
  -> execution/health/audit event recorded for parent review
```

## Governance contract

### Source of truth

Parent Aphelion state remains authoritative for:

- `core.DurableAgent` record,
- live policy and `policy_hash`,
- bootstrap LLM config and ceilings,
- child profile files,
- capability grants/requests,
- operation and continuation gates,
- review target chat,
- child health and audit events.

The Synth process should never carry an independent policy file that can diverge from parent state. Its local flags/config only describe transport admission and token path.

### Startup checks

The Synth process must refuse to start when any required check fails:

- agent exists and `AgentID == synth`,
- `ChannelKind == telegram_group`,
- status is active,
- policy exists and normalizes cleanly,
- bootstrap is configured,
- token file exists, is not world-readable, and is read only at runtime,
- chat allowlist includes exactly the intended group for v0,
- review target chat is configured,
- child local roots resolve under durable-agent state/workspace roots,
- no global/private parent prompt context is injected into child scope.

### Per-turn checks

Before every turn, reload or verify:

- durable agent row still active,
- policy version/hash has not been revoked or downgraded beyond the runner's loaded state,
- capability grants needed by requested action are active,
- inbound chat id matches allowlist,
- sender policy is admissible,
- outbound mode permits local reply.

If checks fail: do not call the model, do not send group reply, record a blocked health event.

### Policy updates

Parent policy updates should be visible on the next turn without editing the Synth unit.

Implementation options:

- simplest: reload durable-agent row/profile/bootstrap from the shared SQLite store on every update before child execution;
- stricter: store last applied `policy_hash` in Synth runner state and compare before/after turn;
- future remote mode: reuse `durable-agent remote sync` policy poll/ack semantics if the runner moves out-of-process or off-host.

### Capability gates

Token presence is not capability.

Synth may have staged credentials for Telegram and Gmail, but actions remain gated:

- Telegram token permits transport only.
- Gmail OAuth permits later read-only mailbox access only after a Synth email-intake grant.
- Public web search requires a public-web/read grant.
- CV/profile retention requires Mada consent and a private-data-intake grant.
- Job applications, recruiter contact, email send, archive/delete, and public posting are separate high-risk gates.

### Health and audit

The Synth process should emit parent-visible, high-level events:

- startup success/failure,
- policy hash/version loaded,
- Telegram getMe identity check result, without token exposure,
- admitted/ignored/blocked update counts,
- child turn success/failure,
- outbound message id when a reply is sent,
- last blocked reason,
- capability request/report ids,
- no-CV/no-email/no-web status until those phases are approved.

Admin health summaries should be concise and route to Daniel's admin chat, not the group.

## Concrete repo changes

### Config

Add a narrow config model rather than extending the global bot immediately:

- `config/config.go`
  - add `SynthTelegramConfig` or generic `TelegramChildBotConfig` if the seam is clean;
  - fields: `agent_id`, `token_file`, `chat_id`, `respond_on`, `review_target_chat_id`, `enabled`;
  - validate: required fields, valid agent id, nonzero chat id, `respond_on in all|mentions`, token_file path present but do not log contents.

Recommended TOML shape:

```toml
[[telegram.child_bots]]
agent_id = "synth"
token_file = "/home/sadasant_gmail_com/.aphelion/secrets/synth/telegram-bot-token"
chat_id = -5056905988
respond_on = "mentions"
review_target_chat_id = 6313146
enabled = true
```

For v0, the command flags may be enough; the config shape should still be documented so the runner can become declarative later.

### Main command

- `main.go`
  - add maintenance/runtime command dispatch for `synth-telegram` or `telegram-child-bot`.
- New file likely: `main_synth_telegram.go` or `main_telegram_child_bot.go`
  - parse flags/config;
  - open existing config/store/resolver;
  - load and validate durable agent;
  - read token from token_file only after validation reaches transport start;
  - construct Telegram client/poller for Synth token;
  - route updates to the existing runtime durable group child path.

### Runtime seam

Prefer reusing existing code rather than forking child execution:

- `runtime/durable_group.go`
  - reuse `RunDurableTelegramGroupChild` for child cognition;
  - add a safe helper if current method assumes main runtime sender/client.
- `runtime/durable_child.go`
  - confirm the sandbox durable group child executor still strips parent Telegram config from child bootstrap.
- `telegram/poller.go` and `telegram/durable_groups.go`
  - reuse durable group route/admission logic or extract a route for one child bot.
- `telegram/client.go`
  - no token logging; `getMe` is okay for identity validation when approved.

### Store/state

Use existing durable-agent store where possible. Add minimal runner state only if needed:

- last start time,
- last policy hash loaded,
- last update id/offset if not already held by Telegram polling semantics,
- last health event / blocked reason.

Prefer execution events over ad hoc files so `/status` and `/doctor` can project it.

### Docs

Update or link from:

- `docs/architecture/durable-children.md`
- `README.md` durable Telegram group section
- new doc: this file

Document:

- transport-only invariant,
- token staging path,
- onboarding gate,
- child profile/memory isolation,
- health route,
- failure modes.

## Tests

### Config tests

Add tests in `config/config_test.go`:

- parses `[[telegram.child_bots]]` with Synth fields;
- rejects missing `agent_id`, `token_file`, `chat_id`;
- rejects invalid `respond_on`;
- rejects duplicate child bot chat/agent combinations;
- does not require or expose token contents during config load.

### Command/startup tests

Add tests near main command tests:

- missing token file fails closed before polling;
- world-readable token file fails closed;
- unknown/non-active agent fails closed;
- non-telegram_group agent fails closed;
- missing bootstrap/policy fails closed;
- startup records health/audit failure without token text.

### Poller/admission tests

Add tests in `telegram/durable_groups_test.go` or new child bot tests:

- configured chat id admits messages only from `-5056905988`;
- other group ids are ignored;
- `respond_on=mentions` ignores unaddressed chatter;
- mention/reply wakes Synth;
- normalized inbound sets `DurableAgentID="synth"`;
- no private DM handling in Synth runner v0.

### Runtime governance tests

Add tests around `runtime/durable_group_runtime_test.go`:

- child bot runner calls existing durable child executor, not a direct model provider;
- policy hash/version is loaded before turn;
- policy revoked/stale/missing blocks before model call;
- outbound reply is denied when Synth policy is `read_only`;
- capability-required actions become request/report artifacts, not direct execution;
- child prompt does not include parent private DM transcript.

### Health/status tests

Add tests in status/doctor surfaces as appropriate:

- Synth child bot health appears with running/stopped/blocked status;
- last policy hash and last blocked reason are visible;
- health summary redacts token path details if needed and never prints token contents;
- no CV/email/web capability active appears until grants exist.

### Integration/smoke tests

Use fake Telegram client; do not hit Telegram in normal tests.

- fake `getUpdates` produces a group mention;
- fake child executor returns a reply;
- fake Telegram sender records outbound through Synth client;
- verify message id recorded and no group history read path is called.

Live smoke tests require explicit operator lease:

1. token metadata check only;
2. `getMe` identity check for Synth bot;
3. start process with `respond_on=mentions` and no outgoing message;
4. optional single approved onboarding send after Mada is ready.

## Validation gates

Implementation should land in gated phases:

1. **Plan doc**: this document; no runtime effects.
2. **Code/config support**: compile and unit tests; no token use.
3. **Token metadata + config preflight**: file exists/mode check; no Telegram call.
4. **Telegram identity smoke**: `getMe` only; no group message.
5. **Runner dry-start**: starts and ignores group unless mentioned; no outbound.
6. **Onboarding send**: one approved message in group.
7. **Mada intake**: consent/profile phase.
8. **Email/job ranking**: later grants only.

Repo-only status/preflight commands are deliberately metadata-only:

```bash
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent synth --status
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent synth --preflight
```

Both commands must stop after route selection, token-file metadata checks, durable-agent lookup, live-policy presence, and bootstrap presence. They must not read token contents, call Telegram, poll updates, patch config, restart services, or send group messages. Their output should include `token_file_status`, `durable_agent_status`, `channel_kind`, `live_policy_status`, `bootstrap_status`, and the next gate (`get-me-smoke_requires_separate_live_approval`) without printing the token path or token contents.

The next live command is separately gated:

```bash
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent synth --get-me-smoke
```

That command may read the token and call Telegram `getMe` exactly once, but must not poll, send, or read group history.

The live runner/dry-start gate has a no-send mode:

```bash
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent synth --dry-start
aphelion telegram-child-bot --config ~/.aphelion/aphelion.toml --agent synth --no-send
```

`--dry-start` constructs the runtime with no-send outbound, prints readiness, and exits without reading token contents, calling Telegram, or polling. `--no-send` is for later live polling tests: it may read the token and poll as separately approved, but routes admitted child turns through no-send delivery so no group reply can be emitted before the onboarding gate.

Each gate must report:

- files changed,
- tests run,
- live actions taken/not taken,
- residual risk,
- next blocked approval.

## Failure behavior

Fail closed on:

- missing/weak token file permissions,
- parent durable-agent record unavailable,
- inactive/non-telegram agent,
- policy hash mismatch without successful reload,
- missing bootstrap,
- unknown chat id,
- unsupported sender/admission state,
- child executor unavailable,
- capability denial,
- outbound policy denial,
- send failure.

Failure should emit a health/audit event to parent review target and should not post diagnostic detail into the family group unless explicitly approved.

## Rollback

- Stop/disable the child bot user service installed from `deploy/aphelion-telegram-child-bot.service`.
- Remove or disable `[[telegram.child_bots]]` entry if added.
- Revoke Synth Telegram token in BotFather if compromised.
- Keep durable child `synth` state intact unless Daniel/Mada request deletion.
- Revoke `synth@idolum.ai` OAuth token separately if email intake should stop.


## Polished launch readiness plan

A polished launch means Synth is not merely reachable. It is understandable to Mada, inspectable by Daniel, fail-closed under Aphelion governance, and recoverable without guessing from chat history.

### Launch domains

#### 1. Product promise

- Synth introduces itself as a small job-search helper, not a general family assistant.
- Synth explains what it can do now, what is not enabled yet, and how Mada can pause or correct it.
- Synth asks for preferences organically after consent instead of assuming Daniel's job-search profile.

#### 2. Consent and privacy

- Mada explicitly opts in before CV/profile intake.
- Daniel-visible health summaries stay high-level and avoid raw wife data.
- No group history import occurs unless explicitly approved by participants.
- Retention/deletion language is part of the onboarding script.

#### 3. Visible Telegram identity

- Synth uses its own BotFather token and visible Telegram bot identity.
- The main Aphelion bot token is not replaced and does not speak as Synth.
- The Synth runner admits only the configured group for v0.
- First outbound group message is a separately approved one-shot onboarding send.

#### 4. Runtime architecture

- Ship a narrow `telegram-child-bot`/`synth-telegram` command in the shared Aphelion binary.
- Run Synth as a separate systemd user service for process-level isolation.
- Do not start parent-only loops: admin commands, heartbeat, cron, nocturne, startup recovery, durable wake loop, tailnet parent server.
- Reuse the existing durable telegram child executor for cognition and policy.

#### 5. Governance and capability gates

- Parent Aphelion durable-agent state is the source of truth for policy, bootstrap, profile, grants, and review lanes.
- The transport runner cannot call a model directly or grant capabilities to itself.
- Token presence never equals authority: Telegram, Gmail, web, CV, digest, archive, send, apply, and public-contact powers remain separately gated.
- Policy/stale/missing/bootstrap mismatch blocks before model call or outbound reply.

#### 6. Child isolation

- Synth runs with durable-agent principal identity and Synth-local workspace/memory/profile roots.
- Parent private DM context and Daniel job-search material are not injected into Synth.
- Approved parent guidance flows through explicit child profile, artifacts, policy, or parent-conversation messages.
- Shared memory writes from Synth require governed paths.

#### 7. Configuration and secrets

- Add explicit child-bot config fields: `agent_id`, `token_file`, `chat_id`, `respond_on`, `review_target_chat_id`, `enabled`.
- Validate token file presence and permissions by metadata only; never log token contents.
- Keep Synth Telegram token under `~/.aphelion/secrets/synth/telegram-bot-token`.
- Keep Synth Gmail account bound to `client=synth`, `account=synth@idolum.ai`.

#### 8. Tests

- Config tests cover child-bot parsing, required fields, duplicate routes, respond_on validation, and no secret exposure.
- Startup tests cover missing token, weak token permissions, unknown/inactive/wrong-channel agent, missing policy/bootstrap, and fail-closed health events.
- Poller tests cover chat allowlist, mentions/replies only, ignored private DMs, DurableAgentID injection, and no group-history read.
- Runtime tests prove existing durable child executor is used, policy is loaded before turn, read_only blocks local reply, and parent context is not leaked.
- Fake Telegram integration tests cover update -> Synth durable turn -> approved reply through fake Synth sender.

#### 9. Validation gates

- Gate 0: doc plan only.
- Gate 1: code/config/tests with fake clients only.
- Gate 2: token metadata and config preflight only.
- Gate 3: Telegram `getMe` identity smoke only.
- Gate 4: runner dry-start with no group outbound.
- Gate 5: one approved onboarding message.
- Gate 6: consented profile intake.
- Gate 7: email/job ranking grants.
- Gate 8: digest/scouting grants if Mada engages.

#### 10. Observability and `/doctor`

- `/doctor` must show Synth as a distinct projection, not as hidden internal thought or stale progress.
- Report binary version, service status, child-bot config status, policy hash/version, token metadata status, last update, last blocked reason, last outbound id, and active grants.
- Separate surfaces remain visibly separate: internal deliberation, live progress, final replies, recovery, and `/doctor` must not masquerade as each other.
- Health summaries to Daniel remain concise and high-level; family group diagnostics stay silent unless approved.

#### 11. Recovery and restart hygiene

- Synth runner writes durable startup/shutdown/blocked result events so recovery does not infer from chat history.
- Service restart does not replay old group messages or resend onboarding.
- Telegram offset handling avoids duplicate replies after crash.
- Approved continuations remain parent-owned and are not silently consumed by Synth.

#### 12. Deployment and rollback

- Install as a separate user service, e.g. `aphelion-synth-telegram.service`.
- Deploy parent Aphelion binary once; run parent and Synth services from the same build.
- Rollback stops/disables Synth service and child-bot config without deleting durable child state.
- Token revocation and Gmail OAuth revocation remain separate explicit procedures.

#### 13. Launch runbook

- Repo-only check: run unit tests and inspect `/doctor` source projection; no runtime effects.
- Status: run `telegram-child-bot --status`; expect metadata-only health fields and no token read.
- Preflight: run `telegram-child-bot --preflight`; verify config, token metadata, durable child policy/bootstrap, Gmail auth status, and no active email/web/CV grants.
- Smoke: after separate live approval, run `--get-me-smoke`; verify Synth bot identity; no polling, outbound, or group history.
- Dry-start: after separate approval, run `--dry-start` first; then use `--no-send` for live polling tests and verify no outbound even when mentioned.
- Service template: start from `deploy/aphelion-telegram-child-bot.service`; set `@RUN_FLAGS@` to `--no-send` for dry live polling and remove it only under the separate onboarding/reply gate.
- Onboarding: Daniel approves one message; Synth posts; Daniel receives high-level health summary.
- Intake: Mada opts in; Synth asks profile questions; no email/web until later gates.
- First digest: only after approved profile + email/web grant + test job forwards.

#### 14. Acceptance criteria

- Mada sees a bot named Synth, not Aphelion.
- Synth replies only in the approved group and only when policy allows.
- Daniel can inspect health and blocked reasons from `/doctor` or admin summaries.
- Failures stop quietly with parent-visible evidence, not noisy group diagnostics.
- No private parent context, CV data, mailbox content, web search, applications, or recruiter contact occurs without a matching grant.

## Open questions before implementation

- Should `telegram-child-bot` be generic from the first patch, or hard-coded/narrow for Synth and generalized after first proof?
- Should the runner read child-bot config from `aphelion.toml` or only command flags for v0?
- Where should child bot health live: execution events only, durable-agent state, or both?
- Do we want an explicit `transport_only` policy bit, or is that enforced by command structure/tests?
- Should `respond_on=mentions` remain through onboarding, or should onboarding be a separate one-shot command?

## Recommended first implementation slice

Implement a generic but narrow `telegram-child-bot` command with one route, fake-client tests, and fail-closed startup checks.

Do not implement email intake, public web search, daily digest, or group onboarding in this slice.
