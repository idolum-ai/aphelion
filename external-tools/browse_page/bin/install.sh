#!/usr/bin/env bash
set -euo pipefail

state_dir=".aphelion/external-tools/browse_page"
mkdir -p "${state_dir}"
printf 'installed\n' > "${state_dir}/installed"
echo "browse_page fixture installed"
