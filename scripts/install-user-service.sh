#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -n "${APHELION_CONFIG:-}" ]]; then
  config_path="${APHELION_CONFIG}"
elif [[ -f "$HOME/.aphelion/aphelion.toml" ]]; then
  config_path="$HOME/.aphelion/aphelion.toml"
else
  config_path="$HOME/.aphelion/aphelion.toml"
fi
service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
service_path="${service_dir}/aphelion.service"
exec_path="${APHELION_EXEC:-${repo_root}/bin/aphelion}"
workdir="${APHELION_WORKDIR:-${repo_root}}"

mkdir -p "${service_dir}"

"${exec_path}" --config "${config_path}" --check-config
"${exec_path}" init --config "${config_path}"

sed \
  -e "s|@WORKDIR@|${workdir}|g" \
  -e "s|@EXEC_PATH@|${exec_path}|g" \
  -e "s|@CONFIG_PATH@|${config_path}|g" \
  "${repo_root}/deploy/aphelion.service" > "${service_path}"

systemctl --user daemon-reload
if systemctl --user is-active --quiet aphelion; then
  "${exec_path}" park-restart --config "${config_path}" --source install_user_service
  systemctl --user restart aphelion
else
  systemctl --user enable --now aphelion
fi
for _ in {1..10}; do
  if systemctl --user is-active --quiet aphelion; then
    break
  fi
  sleep 1
done
systemctl --user is-active --quiet aphelion
"${exec_path}" verify-deploy --config "${config_path}" --format=kv

echo "Installed, started, and verified user service at ${service_path}"
echo "Manage with: systemctl --user status aphelion"
