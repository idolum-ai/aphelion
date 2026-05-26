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
ledger from the operator channel. `/agents` is the lifecycle board: inspect the
child, request a bounded `Brief`, `Park` it, `Resume` it after activation checks,
or confirm `Retire` when it should leave active use.

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

If an isolated external-channel wake fails before the child can process its
turn, the parent records `wake_failed` in the child's external-channel runtime
state, increments failure/backoff, and queues a bounded review artifact. Pending
parent-conversation messages remain unacknowledged until a later successful
wake actually receives and processes them. This keeps retry behavior quiet
without pretending the child saw instructions it never received.

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

## Remote Host Over Tailnet

Use this path when a durable child needs to operate on a macOS or Linux host
that is reachable over the same Tailnet, but that host should not become a
remote Aphelion child. The remote host is only a work surface. The parent
Aphelion keeps the authority ledger, the durable child receives a bounded
`local_device` grant, and each `remote_host` tool invocation is recorded against
that grant.

V1 uses OpenSSH over the Tailnet, not Tailscale SSH policy mutation. Configure
`tailscale.ssh_path` if the parent should use a non-default SSH binary; otherwise
Aphelion uses `ssh`. `tailscale.ssh_command_timeout` is the remote SSH command
timeout unless the tool input or grant supplies a smaller timeout; it defaults
to 15 minutes so `codex_exec` has room to finish. Aphelion does not auto-accept
host keys or disable SSH host-key checking. First contact must be set up
intentionally by the operator, for example by connecting once from the parent
host or managing `known_hosts` through normal SSH administration.

macOS setup:

- Tailscale is connected on the macOS host.
- Remote Login is enabled in macOS sharing settings.
- The parent Linux host can SSH to the macOS host by MagicDNS name or Tailnet
  IP, and the host key is trusted intentionally.
- `codex_exec` requires Codex to be installed and authenticated on the remote
  host. Remote Codex credentials remain on that host; Aphelion does not copy
  parent Codex credentials into macOS.

Linux remote-host setup is the same shape: Tailscale connected, OpenSSH
reachable from the parent, intentional host-key trust, and Codex installed on
the remote host when `codex_exec` is needed.

The child requests one capability grant for the host, user, workdir prefix, and
TTL. A `codex_exec` request for 15 minutes might look like:

```json
{
  "action": "request_submit",
  "kind": "local_device",
  "target_resource": "tailnet_host:mac-mini",
  "purpose": "Run Codex on mac-mini as daniel under /Users/daniel/Code for 15 minutes.",
  "risk_class": "local_device",
  "contract": {
    "remote_host": {
      "hosts": ["mac-mini"],
      "users": ["daniel"],
      "workdir_prefixes": ["/Users/daniel/Code"],
      "allowed_sandboxes": ["read-only", "workspace-write"],
      "codex_home": "/Users/daniel/.codex",
      "max_timeout_sec": 900
    }
  },
  "constraints": {
    "tool_invocation": {
      "actions": {
        "codex_exec": {
          "selectors": {
            "host": ["mac-mini"],
            "user": ["daniel"]
          },
          "allowed_fields": ["workdir", "prompt", "sandbox", "codex_home", "model", "timeout_sec"]
        }
      }
    }
  }
}
```

After review, the admin activates the grant once:

```json
{
  "action": "grant_set",
  "request_id": "<request_id>",
  "principal": "durable_agent:<agent_id>",
  "kind": "local_device",
  "target_resource": "tailnet_host:mac-mini",
  "allowed_actions": ["codex_exec"],
  "expires_in_seconds": 900
}
```

The same grant can include both `ssh_exec` and `codex_exec` when the request and
review justify both actions:

```json
{
  "allowed_actions": ["ssh_exec", "codex_exec"]
}
```

Once active, the existing grant-activation wake tells the child that the lane is
available. Expiry, revocation, stale grants, host/user/workdir drift, sandbox
drift, and `tool_invocation` selector mismatches all fail closed. Tailnet
reachability by itself is never permission.

Example remote Codex invocation from the child:

```json
{
  "action": "codex_exec",
  "host": "mac-mini",
  "user": "daniel",
  "workdir": "/Users/daniel/Code/aphelion",
  "sandbox": "workspace-write",
  "prompt": "Review the current branch and summarize the risky changes."
}
```

Example raw SSH invocation:

```json
{
  "action": "ssh_exec",
  "host": "mac-mini",
  "user": "daniel",
  "workdir": "/Users/daniel/Code/aphelion",
  "command": "git status --short"
}
```

## Operate

- `/agents`: inspect children, request a bounded brief, park/resume lifecycle,
  confirm retirement, and analyze read-only board triage.
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
