#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_path="${APHELION_CONFIG:-$HOME/.config/aphelion/config.toml}"
service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
service_path="${service_dir}/aphelion.service"
exec_path="${APHELION_EXEC:-${repo_root}/bin/aphelion}"
workdir="${APHELION_WORKDIR:-${repo_root}}"

mkdir -p "${service_dir}"

"${exec_path}" --config "${config_path}" --check-config

sed \
  -e "s|@WORKDIR@|${workdir}|g" \
  -e "s|@EXEC_PATH@|${exec_path}|g" \
  -e "s|@CONFIG_PATH@|${config_path}|g" \
  "${repo_root}/deploy/aphelion.service" > "${service_path}"

systemctl --user daemon-reload
systemctl --user enable --now aphelion

echo "Installed user service at ${service_path}"
echo "Manage with: systemctl --user status aphelion"
