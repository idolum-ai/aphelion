#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_path="${APHELION_CONFIG:-$HOME/.config/aphelion/config.toml}"

cd "${repo_root}"
git pull --ff-only
mkdir -p bin
go build -o bin/aphelion .
"${repo_root}/bin/aphelion" --config "${config_path}" --check-config
systemctl --user restart aphelion

echo "Updated and restarted aphelion using ${config_path}"
