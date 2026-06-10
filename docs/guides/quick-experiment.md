# Quick Experiment

This path gets a new operator to a real Telegram-controlled Aphelion service
with conservative defaults. It assumes Linux, a Telegram account, a bot token,
and one provider API key.

## What You Need

- Linux with `systemd --user`
- a Telegram bot token from BotFather
- your Telegram user ID
- one provider key, such as OpenAI or Anthropic
- a shell on the machine that will run Aphelion

## Install

Install the current public release binary by pinning both the installer ref and release asset to the same tag:

```bash
APHELION_VERSION=v0.1.3
curl -fsSL "https://raw.githubusercontent.com/idolum-ai/aphelion/${APHELION_VERSION}/scripts/install-release.sh" | bash -s -- "${APHELION_VERSION}"
```

If you are running the setup from the same Telegram account that will administer
the bot, use admin detection:

```bash
~/.local/bin/aphelion quickstart --detect-admin --install-service
```

For headless setup, pass the minimum values explicitly:

```bash
APHELION_TELEGRAM_BOT_TOKEN=123:abc \
OPENAI_API_KEY=sk-... \
~/.local/bin/aphelion quickstart --admin-user-id 123456789 --provider openai --install-service
```

The config is written to `~/.aphelion/aphelion.toml` with private file
permissions. Existing configs are kept unless you pass `--force`.

## Verify

Check the service:

```bash
systemctl --user status aphelion
~/.local/bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
```

Open Telegram and send:

```text
/start
/health
/status
```

The healthy path is simple: `/health` can show whether the service is ready,
`/status` shows active and pending work, and `/help` shows the current command
menu.

## First Five Minutes

Use the main chat for one thing at a time. When you want a separate work lane,
start a side thread:

```text
/thread summarize this repo's install path
```

Side-thread replies are prefixed with `(thread N)`, and replies to those
messages continue in that lane. Use `/threads` to see open lanes and `/absorb N`
to close one after the outcome is no longer active.

Use `/context` and `/memory` when you want to inspect what is shaping the next
reply. Both are read-only by default. Use `/mission` to review objective
candidates and `/model` as an admin surface for model-routing choices.

## First Safe Turn

Start with a low-risk request, such as asking Aphelion to summarize its current
status or inspect a small local file. The quickstart config keeps normal turns
at `ask_first`, so bounded actions ask before proceeding.

If something looks wrong:

```text
/stop
/new
/health
```

Use `/stop` to cancel current work in the chat. Use `/new` to start a fresh chat
session without clearing memory.

## Temporary Approval Windows

When you approve a request, the approved Telegram message offers an
`Approve 15m` button. Use it when you want less approval friction for the bounded
task already in front of you. The window opens the temporary automation gate and the
approval grant for new approval requests, scoped to the current chat or side thread.

The active window then offers `Double time` and `Cancel approvals`. Use
`/health` after changing approval windows when you want the service state visible.

## Internet From Isolated Work

The quick path keeps isolated work on `network = "deny"`. That is expected.
Only install the sandbox network helper if a profile needs direct internet
access to a small list of destinations. Use
[Sandbox Networking](sandbox-networking.md) for that path.

## Next

- [Telegram Operations](telegram-operations.md) explains daily control from
  Telegram.
- [Operator Setup](operator-setup.md) explains config, service, deploy, and
  recovery details.
