#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

ledger="docs/architecture/structural-hygiene.md"
threshold=800
fail=0

if [[ ! -f "$ledger" ]]; then
  echo "missing structural hygiene ledger: $ledger" >&2
  exit 1
fi

while IFS= read -r file; do
  lines="$(wc -l <"$file" | tr -d ' ')"
  if (( lines <= threshold )); then
    continue
  fi
  if ! rg -qF "\`$file\`" "$ledger"; then
    echo "large file missing structural hygiene ledger entry: $file has $lines lines" >&2
    fail=1
  fi
done < <(
  find . \
    -path './.git' -prune -o \
    -path './third_party' -prune -o \
    -name '*.go' ! -name '*_test.go' -print |
    sed 's#^\./##' |
    sort
)

while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  if [[ ! -f "$path" ]]; then
    echo "structural hygiene ledger references missing file: $path" >&2
    fail=1
  fi
done < <(
  rg --no-filename -o '`[^`*]+\.go`' "$ledger" |
    sed 's/^`//; s/`$//' |
    grep -v '^_test\.go$' |
    sort -u
)

if (( fail != 0 )); then
  echo "structural taste check failed" >&2
  exit 1
fi

echo "structural taste check passed"
