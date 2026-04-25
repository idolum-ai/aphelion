#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

required_docs=(
  "docs/architecture/README.md"
  "docs/architecture/package-ownership.md"
  "docs/architecture/turn-lifecycle.md"
  "docs/architecture/constitution-and-delivery.md"
  "docs/architecture/durable-children.md"
  "docs/architecture/state-surfaces.md"
  "docs/architecture/coordinator-boundary-audit.md"
  "docs/architecture/migration-appendix.md"
  "docs/architecture/diagrams/README.md"
  "docs/architecture/diagrams/src/README.md"
  "docs/architecture/diagrams/generated/README.md"
)

for file in "${required_docs[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing required architecture doc: $file" >&2
    exit 1
  fi
done

diagram_bases=(
  "01-package-map"
  "02-interactive-turn-sequence"
  "03-constitutional-flow"
  "04-durable-topology"
  "05-state-surfaces"
  "06-delivery-polymorphism"
  "07-present-vs-intended"
)

for base in "${diagram_bases[@]}"; do
  path="docs/architecture/diagrams/${base}.svg"
  if [[ ! -f "$path" ]]; then
    echo "missing canonical architecture diagram: $path" >&2
    exit 1
  fi
done

if rg -n "tmp-diagrams/" \
  --glob '!*.png' \
  --glob '!*.svg' \
  README.md requirements runtime turn pipeline docs/architecture constitution_live_test.go .gitignore Makefile >/dev/null; then
  echo "found legacy tmp-diagrams references outside diagram archive" >&2
  exit 1
fi

echo "architecture docs check passed"
