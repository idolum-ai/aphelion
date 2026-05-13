# Operator Setup

This path is for someone comfortable with Linux services, config files, and
local verification. It keeps Aphelion as a small local service with Telegram as
the radio link.

## Install Choice

Use the release path when you want the current published binary:

```bash
curl -fsSL https://raw.githubusercontent.com/idolum-ai/aphelion/main/scripts/install-release.sh | bash
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
./bin/aphelion init --config ~/.aphelion/aphelion.toml
systemctl --user restart aphelion
./bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
```

Treat a failed `verify-deploy` as a failed deploy. Check the service logs, fix
the cause, and run the gate again.

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
