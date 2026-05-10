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

cd "${repo_root}"
git pull --ff-only
mkdir -p bin
go build -o bin/aphelion .
"${repo_root}/bin/aphelion" --config "${config_path}" --check-config
"${repo_root}/bin/aphelion" init --config "${config_path}"
"${repo_root}/bin/aphelion" park-restart --config "${config_path}" --source source_update
systemctl --user restart aphelion
for _ in {1..10}; do
  if systemctl --user is-active --quiet aphelion; then
    break
  fi
  sleep 1
done
systemctl --user is-active --quiet aphelion
"${repo_root}/bin/aphelion" verify-deploy --config "${config_path}" --format=kv

echo "Updated, restarted, and verified aphelion using ${config_path}"
