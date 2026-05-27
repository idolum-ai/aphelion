# Aphelion

A governed outpost for personal agents.

Aphelion exists for the moment when a conversation has to touch the world: a
file, a service, a memory, a machine far from the laptop. Before capability
becomes action, Aphelion makes authority explicit. It keeps a Telegram radio
link to a local Linux service, records consent and execution as typed evidence,
and gives the operator short paths to ask, act, stop, recover, and inspect what
happened.

Aphelion is the harness, not the speaking identity. An agent may have a voice;
Aphelion is the floor under that voice: the ledger, sandbox, service boundary,
and recovery path that keep action honest.

It is not a programming-only agent, an IDE, a generic assistant, a marketplace,
or a broad channel platform.

## Principles

- **Outpost, not platform.** Keep the system legible enough to govern,
  composable enough to understand under pressure, and durable enough to trust.
- **Radio link, not omnichannel.** Telegram is the primary operator channel; the
  CLI and systemd remain the local repair tools.
- **Ledger, not vibes.** Authority, consent, leases, grants, and evidence are
  records; text is presentation.
- **Authority before capability.** The runtime should know what it is allowed to
  do before it becomes more capable.
- **Short paths to truth.** If work touched the world, the system should be
  able to say what happened, what was checked, and where uncertainty remains.
- **Linux only.** Single target, single binary, no macOS or Windows support.

The full design direction lives in
[docs/architecture/design-principles.md](docs/architecture/design-principles.md).

Aphelion is not small in the absolute sense. Its current surface includes
channels, providers, tools, storage, service management, voice, automation, work
lanes, inspection, credentials, durable agents, Tailnet and remote-host
support, missions, and GitHub App helpers. The outpost constraint is therefore
not minimal LOC or a tiny feature list. It is that every surface remains locally
operable, authority-bounded, evidence-producing, and explainable without turning
into a general agent marketplace or broad channel platform.

## Public Release Provenance

The canonical public source is `github.com/idolum-ai/aphelion`. A separate
historical archive may exist at another location; that archive is not the public
release source of truth. Private pre-public development history is kept out of
the canonical public release because it may contain operational paths,
identifiers, transcripts, or other non-public material.

See [Public Release Provenance](docs/public-release.md) for the release-history
policy.

## Start Here

- New operator: [Quick Experiment](docs/guides/quick-experiment.md)
- Skilled operator: [Operator Setup](docs/guides/operator-setup.md)
- Child agents: [Durable Children](docs/guides/durable-children.md)
- Telegram workflows: [Telegram Operations](docs/guides/telegram-operations.md)
- Contributors: [Contributor Handbook](docs/guides/contributor-handbook.md)
- Full docs map: [docs/README.md](docs/README.md)

## Current Surface

The public surface is broad, but it is grouped into a few governed operator
lanes:

- **Operator channel and inspection:** Telegram approvals, status, recovery,
  evidence, `/context`, `/memory`, mission review, and model-routing controls.
- **Models, voice, and tools:** Anthropic, OpenAI, OpenRouter, Gemini, Ollama,
  Telegram voice transcription, optional ElevenLabs replies, exec, scoped
  file/search/fetch tools, curated memory, session recall, and optional OpenAI
  storage.
- **Persistence and evidence:** SQLite sessions, file-based memory, execution
  evidence, and promise tracking.
- **Automation and work lanes:** heartbeat, cron, bounded auto-approval leases,
  main chat, and side threads with separate context, progress, approvals, and
  recovery state.
- **Service, credentials, and durable agents:** Linux user-service install/update
  scripts, optional GitHub App token helper, configured durable children, daily
  review recipes, Telegram group admission, Tailnet child provisioning, health,
  and inventory surfaces.

Current promise tracking lives in [docs/promises.md](docs/promises.md).

## Fast Install

For a Telegram admin on Linux, pin both the installer ref and release asset to
the current public release tag:

```bash
APHELION_VERSION=v0.1.3
curl -fsSL "https://raw.githubusercontent.com/idolum-ai/aphelion/${APHELION_VERSION}/scripts/install-release.sh" | bash -s -- "${APHELION_VERSION}"
~/.local/bin/aphelion quickstart --detect-admin --install-service
```

For headless setup:

```bash
APHELION_TELEGRAM_BOT_TOKEN=123:abc \
OPENAI_API_KEY=sk-... \
~/.local/bin/aphelion quickstart --admin-user-id 123456789 --provider openai --install-service
```

Other supported providers: `anthropic`, `openrouter`, `gemini`, `ollama`. See
[Operator Setup](docs/guides/operator-setup.md) for configuration details.

`quickstart` writes `~/.aphelion/aphelion.toml` with mode `0600`, validates it,
and refuses to replace an existing config unless `--force` is passed. With
`--install-service`, it also runs the service install path and verifies the
deploy.

Normal turns stay at `ask_first` by default. After manually approving a request,
admins can open a bounded 15-minute approval window from the approved Telegram
message; the inline controls create the temporary automation gate and matching
approval grant together.

## Operate

Operate Aphelion like a small radio room:

- Telegram is for live work, approvals, status, recovery, and evidence.
- CLI/systemd are for install, config checks, service lifecycle, and local
  repair.

Useful gates:

```bash
~/.local/bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml
~/.local/bin/aphelion github-app status --config ~/.aphelion/aphelion.toml
~/.local/bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
systemctl --user status aphelion
journalctl --user -u aphelion -f
```

From Telegram, start with `/health`, `/status`, and `/help`. Use `/thread` when
a second task needs its own lane. Use `/context` and `/memory` to inspect what is
shaping replies. Use `/mission` for objective review and `/model` for admin
model-routing controls. The reference for current commands and buttons is
[docs/telegram-ui-features.md](docs/telegram-ui-features.md).

Isolated work defaults to no network. When a non-admin or durable profile needs
narrow internet access, use the helper-backed path in
[docs/guides/sandbox-networking.md](docs/guides/sandbox-networking.md).

For source checkout work on Linux (requires Go 1.26+; check with `go version`):

```bash
make build
make test
make architecture
```

Aphelion is Linux-only. On macOS or another non-Linux host, use the compile-only
check instead of runtime tests:

```bash
make verify-linux-compile
```

## Architecture

Live package ownership:

- `runtime`: long-lived shell, transport wiring, locks/scopes, background loops,
  durable-agent lifecycle wiring, and concrete port assembly
- `turn`: one-turn state machine, stage ordering, run-kind policy, and
  commit/delivery contracts
- `pipeline`: governor/face conversational transforms and render/floor contract
  helpers

All other packages (`agent`, `config`, `core`, `face`, `prompt`, `provider`,
`session`, `tool`, etc.) are implementation details consumed by `runtime`.

```text
Telegram transport
   -> runtime (shell + adapters)
      -> turn.Machine (stage ordering)
         -> pipeline helpers/contracts (brokerage/floor/render)
      -> session persistence + outbound delivery ports
```

Reference map:

- [docs/architecture/README.md](docs/architecture/README.md)
- [runtime/README.md](runtime/README.md)
- [turn/README.md](turn/README.md)
- [pipeline/README.md](pipeline/README.md)
- [requirements/INDEX.md](requirements/INDEX.md)

## Verify

Before changing behavior on Linux:

```bash
go test ./...
make architecture
make design-principles
make public-readiness
make secrets   # when Gitleaks is installed
git diff --check
```

On non-Linux hosts, `make test` and `make architecture` intentionally stop with
a Linux-only message. Use `make verify-linux-compile` for a static compile check,
then run the full verification loop on Linux before merge.

Run `make design-principles` *(static analysis of authority/consent/control
surfaces)* when touching authority, consent, continuation, wake, goal, status,
or operator-facing control surfaces.

Run `make live-evals` or the narrower `make auto-evals` *(opt-in; spend
provider API calls)* before releases that materially change agency, authority,
proactive mission, or prompt behavior.

## License

[Apache-2.0](LICENSE)
