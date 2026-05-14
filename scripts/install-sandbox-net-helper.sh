#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${APHELION_BIN:-}" ]]; then
  aphelion_bin="$APHELION_BIN"
elif [[ -x "$repo_root/bin/aphelion" ]]; then
  aphelion_bin="$repo_root/bin/aphelion"
elif command -v aphelion >/dev/null 2>&1; then
  aphelion_bin="$(command -v aphelion)"
elif [[ -x "$HOME/.local/bin/aphelion" ]]; then
  aphelion_bin="$HOME/.local/bin/aphelion"
else
  echo "aphelion binary not found; set APHELION_BIN=/path/to/aphelion" >&2
  exit 1
fi

if [[ "$aphelion_bin" != /* ]]; then
  aphelion_bin="$(cd "$(dirname "$aphelion_bin")" && pwd)/$(basename "$aphelion_bin")"
fi

service_user="${APHELION_USER:-${SUDO_USER:-$(id -un)}}"
service_uid="$(id -u "$service_user")"
service_group="${APHELION_GROUP:-$(id -gn "$service_user")}"

if [[ "$(id -u)" -eq 0 ]]; then
  sudo_cmd=()
else
  sudo_cmd=(sudo)
fi

unit_path="/etc/systemd/system/aphelion-sandbox-net-helper.service"
sysctl_path="/etc/sysctl.d/80-aphelion-sandbox-net.conf"

unit="$(cat <<UNIT
[Unit]
Description=Aphelion sandbox network helper
Documentation=https://github.com/idolum-ai/aphelion
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$aphelion_bin sandbox-net helper serve --socket /run/aphelion/sandbox-net.sock --socket-group $service_group --socket-mode 0660 --allowed-uid $service_uid
Restart=on-failure
RestartSec=2s
RuntimeDirectory=aphelion
RuntimeDirectoryMode=0755
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_SYS_ADMIN CAP_SETUID CAP_SETGID CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_SYS_ADMIN CAP_SETUID CAP_SETGID CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE
RestrictAddressFamilies=AF_UNIX AF_NETLINK AF_INET AF_INET6
ProtectSystem=full
ProtectKernelTunables=yes
ProtectControlGroups=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
UNIT
)"

printf '%s\n' "$unit" | "${sudo_cmd[@]}" tee "$unit_path" >/dev/null
printf '%s\n' 'net.ipv4.ip_forward = 1' | "${sudo_cmd[@]}" tee "$sysctl_path" >/dev/null
"${sudo_cmd[@]}" sysctl -w net.ipv4.ip_forward=1 >/dev/null
"${sudo_cmd[@]}" systemctl daemon-reload
"${sudo_cmd[@]}" systemctl enable --now aphelion-sandbox-net-helper.service

echo "Installed aphelion-sandbox-net-helper.service"
echo "Socket: /run/aphelion/sandbox-net.sock"
echo "Allowed UID: $service_uid ($service_user)"
