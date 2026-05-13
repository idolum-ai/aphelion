#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

required_docs=(
  "docs/architecture/README.md"
  "docs/architecture/agency-evaluation-methodology.md"
  "docs/architecture/influences-and-departures.md"
  "docs/architecture/package-ownership.md"
  "docs/architecture/turn-lifecycle.md"
  "docs/architecture/constitution-and-delivery.md"
  "docs/architecture/durable-children.md"
  "docs/architecture/state-surfaces.md"
  "docs/architecture/structural-hygiene.md"
  "docs/architecture/diagrams/README.md"
  "docs/architecture/diagrams/src/README.md"
  "docs/architecture/diagrams/generated/README.md"
  "docs/promises.md"
)

for file in "${required_docs[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing required architecture doc: $file" >&2
    exit 1
  fi
done

lineage_doc="docs/architecture/influences-and-departures.md"
for phrase in "What Aphelion took" "Where Aphelion stops" "Why Aphelion diverges"; do
  if ! rg -qF "$phrase" "$lineage_doc"; then
    echo "influence ledger missing required phrase: $phrase" >&2
    exit 1
  fi
done

for phrase in "Codex" "Hermes" "OpenClaw"; do
  if ! rg -qF "$phrase" "$lineage_doc"; then
    echo "influence ledger must attribute nearby system: $phrase" >&2
    exit 1
  fi
done

if ! rg -qF "Ralph" "$lineage_doc" &&
  ! rg -qF "Ralph-style loop vocabulary pending exact citation" "$lineage_doc"; then
  echo "influence ledger must attribute Ralph loops or explicitly mark Ralph-style vocabulary pending citation" >&2
  exit 1
fi

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
  echo "found removed tmp-diagrams references outside diagram archive" >&2
  exit 1
fi

if ! rg -q "Provider support for Anthropic, OpenAI, OpenRouter, Gemini, and Ollama \\| implemented" docs/promises.md; then
  echo "promise ledger must track Gemini/Ollama provider status" >&2
  exit 1
fi

if ! rg -q "Native constrained file tools and web fetch \\| implemented" docs/promises.md; then
  echo "promise ledger must track native file/web tool status" >&2
  exit 1
fi

if rg -q "^\\|.*\\| partial \\|" docs/promises.md; then
  echo "promise ledger must not leave broad partial public promises in the narrowed release target" >&2
  rg -n "^\\|.*\\| partial \\|" docs/promises.md >&2
  exit 1
fi

required_promise_rows=(
  "External tools: process/subprocess only | implemented"
  "Tailnet declarations and grant-binding projection | implemented"
  "Mission review without autonomous continuation | implemented"
  "Authority/status/diagnosis consistency | implemented"
)

for row in "${required_promise_rows[@]}"; do
  if ! rg -qF "$row" docs/promises.md; then
    echo "promise ledger missing narrowed status row: $row" >&2
    exit 1
  fi
done

if rg -n "no multi-channel support|No multi-channel\\. Telegram only" README.md requirements/core.md docs/architecture/design-principles.md >/dev/null; then
  echo "architecture docs must allow future compiled-in channel adapters without plugin/channel sprawl" >&2
  rg -n "no multi-channel support|No multi-channel\\. Telegram only" README.md requirements/core.md docs/architecture/design-principles.md >&2
  exit 1
fi

if rg -n "private UI|private web UI|Private Web UI|richer private UI|artifact browser|browser artifact explorer|separate operator console|operator dashboards|maintenance dashboards|private tailnet UI|private status UI|Private Admin UI|minimal HTML" requirements/reliability.md requirements/heartbeat.md >/dev/null; then
  echo "architecture docs must not add web/dashboard operator surfaces" >&2
  rg -n "private UI|private web UI|Private Web UI|richer private UI|artifact browser|browser artifact explorer|separate operator console|operator dashboards|maintenance dashboards|private tailnet UI|private status UI|Private Admin UI|minimal HTML" requirements/reliability.md requirements/heartbeat.md >&2
  exit 1
fi

echo "architecture docs check passed"
