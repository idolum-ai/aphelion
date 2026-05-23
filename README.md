# Aphelion

A minimal, governed outpost for personal agents.

License: Apache-2.0.

Aphelion is built for distance: distance from a laptop, distance across time,
distance across trust boundaries, and distance between intention and action. It
keeps a small Linux service reachable through Telegram, records authority and
execution evidence as typed state, and gives the operator short paths to ask,
act, stop, recover, and inspect what happened.

It is not a programming-only agent, an IDE, a generic assistant, a marketplace,
or a broad channel platform.

## Principles

- **Outpost, not platform.** Keep the system small, durable, and useful.
- **Radio link, not omnichannel.** Telegram is the primary operator channel.
- **Ledger, not vibes.** Authority, consent, leases, grants, and evidence are
  records; text is presentation.
- **Authority before capability.** The runtime should know what it is allowed to
  do before it becomes more capable.
- **Linux only.** Single target, single binary, no macOS or Windows support.

The full design direction lives in
[docs/architecture/design-principles.md](docs/architecture/design-principles.md).


## Public Release Provenance

The canonical public source is `github.com/idolum-ai/aphelion`. A separate
historical archive may exist at `github.com/sadasant/aphelion`; that archive is
not the public release source of truth. Private pre-public development history is
kept out of the canonical public release because it may contain operational
paths, identifiers, transcripts, or other non-public material.

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

- **Channel:** Telegram
- **Providers:** Anthropic, OpenAI, OpenRouter, Google Gemini, local Ollama
- **Tools:** exec, scoped native file/search/fetch tools, curated memory,
  session recall, optional OpenAI storage tools
- **Storage:** SQLite sessions, file-based memory, execution evidence
- **Service:** Linux user service through bundled install/update scripts
- **Voice:** Telegram voice transcription and optional ElevenLabs TTS replies
- **Automation:** heartbeat, cron, bounded auto-approval leases
- **Credentials:** optional GitHub App installation-token checks for
  operator-maintained repository workflows
- **Durable agents:** configured durable children, install-owned daily-review recipe, Telegram group
  admission, Tailnet child provisioning, health and inventory surfaces

Current promise tracking lives in [docs/promises.md](docs/promises.md).

## Fast Install

For a Telegram admin on Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/idolum-ai/aphelion/main/scripts/install-release.sh | bash
~/.local/bin/aphelion quickstart --detect-admin --install-service
```

For headless setup:

```bash
APHELION_TELEGRAM_BOT_TOKEN=123:abc \
OPENAI_API_KEY=sk-... \
~/.local/bin/aphelion quickstart --admin-user-id 123456789 --provider openai --install-service
```

`quickstart` writes `~/.aphelion/aphelion.toml` with mode `0600`, validates it,
and refuses to replace an existing config unless `--force` is passed. With
`--install-service`, it also runs the service install path and verifies the
deploy.

Normal turns stay at `ask_first` by default. After manually approving a request,
admins can open a bounded 15-minute approval window from the approved Telegram
message; the inline controls create the temporary automation gate and matching
approval grant together.

## Operate

Aphelion has two operator surfaces:

- Telegram for live work, approvals, status, recovery, and evidence.
- CLI/systemd for install, config checks, service lifecycle, and local repair.

Useful gates:

```bash
~/.local/bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml
~/.local/bin/aphelion github-app status --config ~/.aphelion/aphelion.toml
~/.local/bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
systemctl --user status aphelion
journalctl --user -u aphelion -f
```

From Telegram, start with `/health`, `/status`, and `/help`. The reference for
current commands and buttons is
[docs/telegram-ui-features.md](docs/telegram-ui-features.md).

Isolated work defaults to no network. When a non-admin or durable profile needs
narrow internet access, use the helper-backed path in
[docs/guides/sandbox-networking.md](docs/guides/sandbox-networking.md).

For source checkout work on Linux:

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

Run `make design-principles` when touching authority, consent, continuation,
wake, goal, status, or operator-facing control surfaces.
