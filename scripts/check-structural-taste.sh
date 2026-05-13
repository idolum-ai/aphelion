#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

ledger="docs/architecture/structural-hygiene.md"
threshold=800
fail=0
required_package_docs=(
  "doc.go"
  "agent/doc.go"
  "config/doc.go"
  "core/doc.go"
  "decision/doc.go"
  "durableagent/doc.go"
  "face/doc.go"
  "governorauth/doc.go"
  "governorbackend/doc.go"
  "internal/doc.go"
  "media/doc.go"
  "memory/doc.go"
  "openai/doc.go"
  "pipeline/doc.go"
  "principal/doc.go"
  "prompt/doc.go"
  "provider/doc.go"
  "runtime/doc.go"
  "session/doc.go"
  "tailnet/doc.go"
  "telegram/doc.go"
  "tool/doc.go"
  "tool/sandbox/doc.go"
  "turn/doc.go"
  "voice/doc.go"
  "workspace/doc.go"
)

if [[ ! -f "$ledger" ]]; then
  echo "missing structural hygiene ledger: $ledger" >&2
  exit 1
fi

for file in "${required_package_docs[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing package ownership doc: $file" >&2
    fail=1
  fi
done

if awk -F'|' '
  /^\|[[:space:]]+`.*\.go`[[:space:]]+\|/ {
    file=$2
    owner=$3
    direction=$4
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", file)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", owner)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", direction)
    if (owner == "" || direction == "") {
      print file
    }
  }
' "$ledger" | rg -q .; then
  echo "structural hygiene ledger has rows without owner concept or split direction" >&2
  awk -F'|' '
    /^\|[[:space:]]+`.*\.go`[[:space:]]+\|/ {
      file=$2
      owner=$3
      direction=$4
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", file)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", owner)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", direction)
      if (owner == "" || direction == "") {
        print file
      }
    }
  ' "$ledger" >&2
  fail=1
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
    -name '*.go' -print |
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
  awk -F'|' '
    /^\|[[:space:]]+`.*\.go`[[:space:]]+\|/ {
      file=$2
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", file)
      gsub(/^`|`$/, "", file)
      print file
    }
  ' "$ledger" | sort -u
)

if (( fail != 0 )); then
  echo "structural taste check failed" >&2
  exit 1
fi

echo "structural taste check passed"
