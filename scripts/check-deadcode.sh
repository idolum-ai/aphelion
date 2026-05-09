#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

output="$(go run golang.org/x/tools/cmd/deadcode@latest -test ./...)"
if [[ -n "$output" ]]; then
  echo "$output" >&2
  echo "dead code check failed" >&2
  exit 1
fi

echo "dead code check passed"
