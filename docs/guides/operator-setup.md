# Operator Setup

This path is for someone comfortable with Linux services, config files, and
local verification. It keeps Aphelion as a small local service with Telegram as
the radio link.

## Install Choice

Use the release path when you want the current published binary. Pin both the installer ref and release asset to the same public release tag:

```bash
APHELION_VERSION=v0.1.3
curl -fsSL "https://raw.githubusercontent.com/idolum-ai/aphelion/${APHELION_VERSION}/scripts/install-release.sh" | bash -s -- "${APHELION_VERSION}"
~/.local/bin/aphelion quickstart --detect-admin --install-service
```

Use the source path when you are working from a checkout:

```bash
make build
./bin/aphelion quickstart --detect-admin
make install-user-service
```

The default config path is:

```text
~/.aphelion/aphelion.toml
```

The user service unit lives under:

```text
~/.config/systemd/user/aphelion.service
```

The config controls Aphelion behavior. The service unit controls how Linux
starts the binary and which config path is passed to it.

## Config Discipline

Use `config.example.toml` as the live schema reference. At minimum, set the
Telegram bot token, one provider key, and the admin principal:

```toml
[principals.telegram]
admin_user_ids = [123456789]
```

Keep roots narrow and intentional:

```toml
[agent]
prompt_root = "~/.aphelion/agent"
exec_root = "/home/user/src/aphelion"
shared_memory_root = "~/.aphelion/agent"
user_workspace_root = "~/.aphelion/state/isolated/workspaces"
user_memory_root = "~/.aphelion/state/isolated/memory"
```

`exec_root` is the default shell scope for the `exec` tool. Do not point it at a
broader tree than you mean to operate inside.

If the source checkout is outside `exec_root` or `prompt_root`, add it as an
admin read-only sandbox path so first-class file tools can inspect it without
falling back to shell commands:

```toml
[sandbox.profiles.admin]
readonly_paths = ["/home/user/src/aphelion"]
```

## Optional GitHub App Credentials

Aphelion can verify and mint short-lived GitHub App installation tokens from an
explicitly configured PEM file. This is for operator-maintained repository
workflows where a GitHub App is narrower than a personal access token.

Keep the private key outside the repo and readable only by the service user:

```bash
mkdir -p ~/.aphelion/secrets/github
chmod 700 ~/.aphelion/secrets ~/.aphelion/secrets/github
chmod 600 ~/.aphelion/secrets/github/maintenance.pem
```

Configure the app explicitly:

```toml
[github]
enabled = true

[[github.apps]]
name = "maintenance"
app_id = 123456
installation_id = 987654
private_key_file = "~/.aphelion/secrets/github/maintenance.pem"
repositories = ["owner/repo"]
permissions = ["metadata:read", "contents:read", "pull_requests:read"]
```

Then check the surface:

```bash
./bin/aphelion github-app status --config ~/.aphelion/aphelion.toml
./bin/aphelion github-app status --config ~/.aphelion/aphelion.toml --online
```

`status` is redacted. `--online` mints and discards an installation token and
records a redacted evidence event. `token --format=git-credential` prints a
one-shot Git credential protocol payload for manual plumbing, for example with
`git credential approve`:

```bash
./bin/aphelion github-app token --config ~/.aphelion/aphelion.toml --app maintenance --show-token --format=git-credential
```

That command is not a persistent Git credential helper. Aphelion does not
currently ship an `aphelion github-app credential-helper` mode; the
`git-credential` format is just `protocol`, `host`, `username`, and `password`
for the minted installation token.

When repository work is approved and the operator has separately installed a
dedicated git credential helper for the configured app, prefer that path-aware
operator-provided helper over a broad personal token. Clear any stale/default
helper first and force git to include the repository path in credential lookup:

```bash
GIT_TERMINAL_PROMPT=0 git \
  -c credential.helper= \
  -c credential.helper=/path/to/operator-provided-github-app-helper \
  -c credential.useHttpPath=true \
  ls-remote https://github.com/owner/repo.git HEAD
```

`credential.useHttpPath=true` is load-bearing for repo-scoped helpers. Without
it, git may ask only for host-level `github.com` credentials; a helper that is
intentionally scoped to paths such as `owner/repo` will decline and git may fail
before any installation token is supplied. The one-shot payload above also does
not include a `path=` field, so it does not by itself demonstrate repo-path
scoped credential storage.

Invalid `gh auth status` is therefore not decisive for Aphelion repository work.
After a bounded external-account grant, check the configured GitHub App route
before declaring GitHub blocked. Never print PEM contents or installation
tokens in chat, logs, memory, or review artifacts.

This v1 does not automatically inject GitHub credentials into ordinary shell or
git execution. It makes the credential source typed, checkable, and ledgered
first; operators can then use a governed, separately installed helper path
explicitly inside the approved work boundary.

## Post-Install Telegram Check

After install or update, use Telegram to confirm the operator surface:

```text
/health
/status
/thread check this side lane
/context
/memory
/mission
/model
```

`/health` and `/status` show readiness and active work. `/thread` verifies that
side lanes are attributable. `/context` and `/memory` should be read-only unless
you explicitly answer a follow-up. `/mission` should show objective review
state, and `/model` should show the configured admin model slots and any
provider-specific speed controls.

## Deploy Gate

The deploy path is always:

1. build or replace the binary
2. validate config
3. seed missing prompt files
4. restart the user service
5. verify the live service

Source checkout:

```bash
make update
```

Release binary:

```bash
make update-release
```

Manual gate:

```bash
./bin/aphelion --config ~/.aphelion/aphelion.toml --check-config
./bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml --format=kv
./bin/aphelion init --config ~/.aphelion/aphelion.toml
systemctl --user restart aphelion
./bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
```

Treat a failed `verify-deploy` as a failed deploy. Check the service logs, fix
the cause, and run the gate again.

If `tailscale.parent.enabled = true`, the parent Tailnet listener is part of
service startup. Missing auth material, invalid tsnet state, or listener startup
failure should stop the service and make the deploy gate fail instead of leaving
remote children without their private control plane.

Keep non-admin and durable sandbox profiles on `network = "deny"` unless
`sandbox-net check` reports the allowlist backend available. For isolated
allowlists, destinations are explicit `host:port`, `ip:port`, or `cidr:port`
entries; hostnames compile to IP/port firewall rules when the process starts.
Use [Sandbox Networking](sandbox-networking.md) when a profile needs this
bounded egress path.

## Sandbox Network Allowlists

The safe default is no isolated egress:

```toml
[sandbox.profiles.approved_user]
mode = "isolated"
network = "deny"
```

Only switch a profile to allowlist after the host check passes:

```bash
./bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml --format=kv
```

Then use explicit destinations:

```toml
[sandbox.profiles.approved_user]
mode = "isolated"
network = "allowlist"
network_allow = ["api.openai.com:443", "github.com:443"]
```

The allowlist backend is a root-owned helper service. The main user service
talks to `/run/aphelion/sandbox-net.sock`; it does not receive network namespace
or firewall capabilities. Install the helper from a checkout with:

```bash
make install-sandbox-net-helper
```

The helper needs Linux network namespaces, nftables, `setpriv`, IPv4
forwarding, `CAP_NET_ADMIN`, and `CAP_SYS_ADMIN`. If any prerequisite is
absent, Aphelion refuses the allowlisted process instead of falling back to host
networking. This is IP/port enforcement; it does not inspect HTTP Host headers
or TLS SNI. The current backend enforces IPv4 egress; IPv6-only destinations
fail closed.

## Inspect

```bash
systemctl --user status aphelion
journalctl --user -u aphelion -f
./bin/aphelion paths --config ~/.aphelion/aphelion.toml
./bin/aphelion durable-agent list --config ~/.aphelion/aphelion.toml
./bin/aphelion authority doctor --config ~/.aphelion/aphelion.toml
```

From Telegram, use `/health` for the compact operational panel and `/status` for
active chat and work state.

Recent Telegram poison updates are visible in system status as
`telegram_ingress_failures`. If that block appears, inspect the update ID, chat
ID, message ID, and error text before assuming the bot is stuck; Aphelion has
already advanced past that update to preserve the radio link.

## Durable Children

Use [Durable Children](durable-children.md) for child setup recipes, including
local, scheduled, Telegram group, external-channel, and Tailnet remote children.

## Maintain

Use garbage collection for bounded maintenance:

```bash
./bin/aphelion gc --config ~/.aphelion/aphelion.toml
```

Use explicit authority repair commands only with a fresh finding ID and an
operator-written reason:

```bash
./bin/aphelion authority repair --config ~/.aphelion/aphelion.toml
./bin/aphelion authority repair --config ~/.aphelion/aphelion.toml --apply --finding af_...
```

Use [Telegram Operations](telegram-operations.md) for the live operator surface
and [Telegram UI Features](../telegram-ui-features.md) for the full reference.
