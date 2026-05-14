# Sandbox Networking

Aphelion keeps isolated execution on `network = "deny"` by default. That is the
right setting for quick experiments and for any child that does not need direct
internet access.

Use `network = "allowlist"` only when a specific isolated profile needs bounded
egress to explicit `host:port`, `ip:port`, or `cidr:port` destinations. The
allowlist is enforced by a small root-owned helper service. The main Aphelion
user service does not receive host networking capabilities.

## Quick Path

Start with the default:

```toml
[sandbox.profiles.approved_user]
mode = "isolated"
network = "deny"
```

Check the current host state:

```bash
~/.local/bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml --format=kv
```

If you do not need isolated internet access, stop here. The check can report the
helper unavailable while deny-only profiles remain ready.

When you do need a narrow allowlist, install the helper from a checkout:

```bash
make install-sandbox-net-helper
```

Then verify:

```bash
./bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml --format=kv
```

Only after the backend is available should you switch a profile:

```toml
[sandbox.profiles.approved_user]
mode = "isolated"
network = "allowlist"
network_allow = ["api.openai.com:443", "github.com:443"]
```

## Operator Path

The helper installer writes:

- `/etc/systemd/system/aphelion-sandbox-net-helper.service`
- `/etc/sysctl.d/80-aphelion-sandbox-net.conf`
- `/run/aphelion/sandbox-net.sock` while the service is running

Set `APHELION_SANDBOX_NET_SOCKET` on the Aphelion user service only if you
intentionally move the socket.

The service runs:

```bash
aphelion sandbox-net helper serve --socket /run/aphelion/sandbox-net.sock --allowed-uid <aphelion-user-uid>
```

The helper accepts two operations:

- `status`: report whether the host can enforce allowlisted networking.
- `run`: create a short-lived network namespace, install nftables rules, run the
  trusted `bwrap` binary as the calling Aphelion UID, capture output, and clean
  up the namespace and rules.

The helper requires `ip`, `nft`, `setpriv`, IPv4 forwarding, `CAP_NET_ADMIN`,
and `CAP_SYS_ADMIN`. Those capabilities stay in the helper service. The user
service talks over the Unix socket and keeps normal user privileges. The helper
must preserve Aphelion's configured sandbox path contract: writable and readonly
roots are decided by the `bwrap` profile, not by making the operator home
read-only in the helper service.

Useful checks:

```bash
systemctl status aphelion-sandbox-net-helper.service
journalctl -u aphelion-sandbox-net-helper.service -f
sysctl net.ipv4.ip_forward
./bin/aphelion sandbox-net check --config ~/.aphelion/aphelion.toml --format=kv
```

This is IP/port enforcement. Hostname entries are resolved when a process starts
and then compiled to IPv4 firewall rules. The helper does not inspect HTTP Host
headers or TLS SNI, and IPv6-only destinations fail closed.
