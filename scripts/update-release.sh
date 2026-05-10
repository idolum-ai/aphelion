#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec_path="${APHELION_EXEC:-$HOME/.local/bin/aphelion}"
if [[ -n "${APHELION_CONFIG:-}" ]]; then
  config_path="${APHELION_CONFIG}"
elif [[ -f "$HOME/.aphelion/aphelion.toml" ]]; then
  config_path="$HOME/.aphelion/aphelion.toml"
else
  config_path="$HOME/.aphelion/aphelion.toml"
fi

"${repo_root}/scripts/install-release.sh" "${1:-}"
"${exec_path}" --config "${config_path}" --check-config
"${exec_path}" init --config "${config_path}"
"${exec_path}" park-restart --config "${config_path}" --source release_update
systemctl --user restart aphelion
for _ in {1..10}; do
  if systemctl --user is-active --quiet aphelion; then
    break
  fi
  sleep 1
done
systemctl --user is-active --quiet aphelion
"${exec_path}" verify-deploy --config "${config_path}" --format=kv

echo "Updated, restarted, and verified release binary at ${exec_path} using ${config_path}"
