#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec_path="${APHELION_EXEC:-$HOME/.local/bin/aphelion}"

"${repo_root}/scripts/install-release.sh" "${1:-}"
systemctl --user restart aphelion

echo "Updated release binary at ${exec_path} and restarted aphelion"
