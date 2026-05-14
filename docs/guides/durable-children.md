# Durable Children

Durable children are bounded child Aphelions. They are useful when a role needs
its own charter, memory, policy, wake cadence, or transport boundary. The parent
keeps governance and evidence; the child works inside the policy it was given.

## Choose A Child Kind

| Kind | Use when | Operator path |
|---|---|---|
| Local or scheduled child | The child can run inside the parent service and wake on a schedule or parent prompt. | Create through the durable setup wizard, then inspect with CLI and `/agents`. |
| Telegram group child | A specific Telegram group should have a child-local charter and policy. | Admit the group as a durable Telegram child and validate group delivery. |
| External-channel child | Another local adapter should observe or report status without becoming a house principal. | Create an external-channel child and keep adapter state under child runtime state. |
| Tailnet remote child | The child should run its own Aphelion service on a Linux Tailnet host. | Configure Tailnet, dry-run `durable-agent provision`, then apply over Tailscale SSH. |

## Shared Setup Shape

Every child setup should leave the same trail:

1. Define the role: agent id, visible name, channel kind, and review target.
2. Write the charter: what the child may do, what it must ask about, and what it
   must never do.
3. Set the bootstrap: child-local model or Codex home, storage roots, secret
   scopes, and network posture.
4. Set the live policy: outbound mode, drift policy, capability envelope, and
   any Tailnet declaration.
5. Activate or provision.
6. Validate with:

```bash
./bin/aphelion durable-agent list --config ~/.aphelion/aphelion.toml
./bin/aphelion durable-agent health --config ~/.aphelion/aphelion.toml --agent <agent_id>
```

From Telegram, use `/agents`, `/status`, and `/health` to check the same parent
ledger from the operator channel.

## Daily Review Recipe

`aphelion init` can install the bundled `daily-review` durable-child recipe as `idolum-daily-review` on the target host. Ordinary runtime startup does not recreate the child if it is removed or disabled; after installation, the target database owns the child row and its policy/bootstrap/local state. The recipe manifest lives at `recipes/durable-children/daily-review.toml`.

## Local Or Scheduled Child

Use this path for a child that does parent-side work, scheduled checks, or daily
review-style summaries without a separate external runtime.

1. In a private admin chat, ask for a durable child and answer the wizard one
   field at a time.
2. Choose a narrow charter and `read_only` or `draft_only` outbound mode unless
   the child has a concrete reviewed action lane.
3. Keep storage roots under the Aphelion state tree unless the role needs a
   separate scoped workspace.
4. Finalize the wizard, then check:

```bash
./bin/aphelion durable-agent health --config ~/.aphelion/aphelion.toml --agent <agent_id>
```

The healthy first state is `active` with a present state record. Wakes and
reviews should appear as durable events, not only chat text.

## Telegram Group Child

Use this path when a group chat should be handled by a child-local charter
instead of the house principal.

1. Add the group admission in `~/.aphelion/aphelion.toml` using the live schema
   in `config.example.toml`.
2. Give the child a local bootstrap. Do not rely on parent provider credentials
   unless the config explicitly gives that child its own allowed bootstrap.
3. Run the deploy gate:

```bash
./bin/aphelion --config ~/.aphelion/aphelion.toml --check-config
./bin/aphelion init --config ~/.aphelion/aphelion.toml
systemctl --user restart aphelion
./bin/aphelion verify-deploy --config ~/.aphelion/aphelion.toml
```

4. Send a low-risk message in the admitted group and inspect `/status` from the
   admin chat.

The child should answer only where policy allows. Parent/admin review remains
the escalation path for broader action.

## External-Channel Child

Use this path when a child observes or reports through a local adapter such as a
read-only status heartbeat.

1. Start from the durable setup wizard or an existing durable-agent record.
2. Choose `external_channel` as the channel kind.
3. Keep adapter cursor, last status, failure count, backoff, and adapter-specific
   payload under the child runtime state.
4. Validate health and review output:

```bash
./bin/aphelion durable-agent health --config ~/.aphelion/aphelion.toml --agent <agent_id>
./bin/aphelion durable-agent list --config ~/.aphelion/aphelion.toml
```

External-channel prompts are payloads inside governed commands. They do not
widen the child capability envelope.

## Tailnet Remote Child

Use this path when the child should run on its own Linux Tailnet host while the
parent keeps governance.

Prerequisites:

- parent Tailnet service enabled in `~/.aphelion/aphelion.toml`
- `tailscale.parent.admin_login_names` set for any operator who should use the
  Tailnet-private JSON mirrors
- durable agent live policy includes a Tailnet mode and `tailnet_hostname`
- Tailscale SSH works from parent host to child host
- the local Aphelion binary has already been built

Dry-run first:

```bash
./bin/aphelion durable-agent provision \
  --config ~/.aphelion/aphelion.toml \
  --agent <agent_id> \
  --binary ./bin/aphelion
```

Apply only when the dry-run output names the intended host, service, root, and
parent control URL:

```bash
./bin/aphelion durable-agent provision \
  --config ~/.aphelion/aphelion.toml \
  --agent <agent_id> \
  --binary ./bin/aphelion \
  --apply
```

Then verify parent-side enrollment and pulse:

```bash
./bin/aphelion durable-agent health --config ~/.aphelion/aphelion.toml --agent <agent_id>
./bin/aphelion tailnet status --config ~/.aphelion/aphelion.toml
```

The child polls the parent Tailnet `/control` plane. The parent does not require
inbound HTTP on the child. The parent records the child's Tailnet stable node ID
when accepting enrollment, or on the first valid accepted control request if an
active enrollment does not yet have a stored node identity. The parent verifies
any declared `tailnet_hostname` and `tailnet_tags`; later control-plane calls
must come from that same Tailnet node and continue satisfying those declared
hostname/tag requirements.

## Operate

- `/agents`: inspect children and open parent-child conversation controls.
- `agent:<agent_id> <message>`: route one Telegram DM message to an active
  durable Telegram child.
- `/status`: inspect active work and durable aggregate health.
- `/health`: inspect compact service readiness and diagnosis controls.
- `durable-agent health`: inspect one child from CLI.

If a child behaves unexpectedly, stop the live turn first with `/stop`, inspect
health, and fix the governing record or config before broadening capability.

## Boundaries

- A child is not a plugin, marketplace entry, or second operator platform.
- Child credentials and local storage roots are child-scoped.
- Parent authority stays parent-owned.
- Tailnet reachability is transport, not permission.
- Review artifacts, parent conversation, health, and TES events are the evidence
  path. Chat text is not the source of authority.
