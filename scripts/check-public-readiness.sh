#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

required_files=(
  "LICENSE"
  ".gitleaks.toml"
  "THIRD_PARTY_NOTICES.md"
  "SECURITY.md"
  "CONTRIBUTING.md"
  ".github/pull_request_template.md"
  ".github/ISSUE_TEMPLATE/bug_report.md"
  ".github/ISSUE_TEMPLATE/feature_request.md"
)

for file in "${required_files[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing public-readiness file: $file" >&2
    exit 1
  fi
done

for forbidden in \
  ".env" \
  "aphelion.toml" \
  "docs/architecture/synth-telegram-bot-subsystem-plan.md"; do
  if git ls-files --error-unmatch "$forbidden" >/dev/null 2>&1 && [[ -e "$forbidden" ]]; then
    echo "forbidden public tracked file: $forbidden" >&2
    exit 1
  fi
done

if git ls-files | rg -n '(^|/)(secrets?|private)(/|$)|\.(db|sqlite|sqlite3|log|pem|key)$' >/dev/null; then
  echo "tracked file looks like a private runtime artifact:" >&2
  git ls-files | rg -n '(^|/)(secrets?|private)(/|$)|\.(db|sqlite|sqlite3|log|pem|key)$' >&2
  exit 1
fi

public_surfaces=(
  "README.md"
  "AGENTS.md"
  "CONTRIBUTING.md"
  "SECURITY.md"
  "THIRD_PARTY_NOTICES.md"
  "config.example.toml"
  "docs"
  "requirements"
)

private_pattern='sadasant|/home/sadasant_gmail_com|synth@idolum\.ai|5056905988|client=synth|account=synth|synth-telegram|Organic Ralph|family-group'

if rg -n --glob '*.md' --glob '*.toml' --glob '!docs/architecture/synth-telegram-bot-subsystem-plan.md' "$private_pattern" "${public_surfaces[@]}" >/dev/null; then
  echo "public docs/config contain private or live-operation markers:" >&2
  rg -n --glob '*.md' --glob '*.toml' --glob '!docs/architecture/synth-telegram-bot-subsystem-plan.md' "$private_pattern" "${public_surfaces[@]}" >&2
  exit 1
fi

echo "public readiness check passed"
