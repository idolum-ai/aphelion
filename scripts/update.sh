#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -n "${APHELION_CONFIG:-}" ]]; then
  config_path="${APHELION_CONFIG}"
elif [[ -f "$HOME/.aphelion/aphelion.toml" ]]; then
  config_path="$HOME/.aphelion/aphelion.toml"
elif [[ -f "$HOME/.config/aphelion/config.toml" ]]; then
  config_path="$HOME/.config/aphelion/config.toml"
else
  config_path="$HOME/.aphelion/aphelion.toml"
fi

cd "${repo_root}"
git pull --ff-only
mkdir -p bin
go build -o bin/aphelion .
"${repo_root}/bin/aphelion" --config "${config_path}" --check-config
"${repo_root}/bin/aphelion" init --config "${config_path}"
systemctl --user restart aphelion

echo "Updated and restarted aphelion using ${config_path}"
