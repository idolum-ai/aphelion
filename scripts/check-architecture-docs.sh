#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

required_docs=(
  "docs/architecture/README.md"
  "docs/architecture/done-done-roadmap.md"
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
  "docs/promises.md"
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

if ! rg -q "Provider support for Anthropic, OpenAI, OpenRouter, Gemini, and Ollama \\| implemented" docs/promises.md; then
  echo "promise ledger must track Gemini/Ollama provider status" >&2
  exit 1
fi

if ! rg -q "Native constrained file tools and web fetch \\| implemented" docs/promises.md; then
  echo "promise ledger must track native file/web tool status" >&2
  exit 1
fi

if ! rg -qF "Operator and user surfaces are limited to:" docs/architecture/done-done-roadmap.md ||
  ! rg -qF "Telegram" docs/architecture/done-done-roadmap.md ||
  ! rg -qF "CLI commands" docs/architecture/done-done-roadmap.md; then
  echo "done-done roadmap must keep operator surfaces limited to Telegram and CLI" >&2
  exit 1
fi

if ! rg -qF "one final release and one live migration/init call" docs/architecture/done-done-roadmap.md; then
  echo "done-done roadmap must document one-release/one-live-migration discipline" >&2
  exit 1
fi

if ! rg -qF "Future channels such as WhatsApp should be ordinary compiled-in code changes" docs/architecture/done-done-roadmap.md; then
  echo "done-done roadmap must preserve future channel extensibility without plugins" >&2
  exit 1
fi

if rg -n "no multi-channel support|No multi-channel\\. Telegram only" README.md requirements/core.md docs/architecture/design-principles.md >/dev/null; then
  echo "architecture docs must allow future compiled-in channel adapters without plugin/channel sprawl" >&2
  rg -n "no multi-channel support|No multi-channel\\. Telegram only" README.md requirements/core.md docs/architecture/design-principles.md >&2
  exit 1
fi

if rg -n "private UI|private web UI|Private Web UI|richer private UI|artifact browser|browser artifact explorer|separate operator console|operator dashboards|maintenance dashboards|private tailnet UI|private status UI|Private Admin UI|minimal HTML" docs/architecture/tailscale-agent-substrate-project.md requirements/reliability.md requirements/heartbeat.md >/dev/null; then
  echo "architecture docs must not add web/dashboard operator surfaces" >&2
  rg -n "private UI|private web UI|Private Web UI|richer private UI|artifact browser|browser artifact explorer|separate operator console|operator dashboards|maintenance dashboards|private tailnet UI|private status UI|Private Admin UI|minimal HTML" docs/architecture/tailscale-agent-substrate-project.md requirements/reliability.md requirements/heartbeat.md >&2
  exit 1
fi

echo "architecture docs check passed"
