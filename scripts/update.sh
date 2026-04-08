#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${repo_root}"
git pull --ff-only
mkdir -p bin
go build -o bin/aphelion .
systemctl --user restart aphelion

echo "Updated and restarted aphelion"
