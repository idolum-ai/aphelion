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
  "docs/public-release.md"
)

for file in "${required_files[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "missing public-readiness file: $file" >&2
    exit 1
  fi
done

for forbidden in \
  ".env" \
  "aphelion.toml"; do
  if git ls-files --error-unmatch "$forbidden" >/dev/null 2>&1 && [[ -e "$forbidden" ]]; then
    echo "forbidden public tracked file: $forbidden" >&2
    exit 1
  fi
done

artifact_path_pattern='(^|/)(secrets?|private)(/|$)|\.(db|sqlite|sqlite3|log|pem|key)$|(^|/)aphelion\.toml$|[[:alnum:]_-]*telegram-bot-subsystem'

if git ls-files | rg -n "$artifact_path_pattern" >/dev/null; then
  echo "tracked file looks like a private runtime artifact:" >&2
  git ls-files | rg -n "$artifact_path_pattern" >&2
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

private_pattern='[[:alnum:]_]+_gmail_com|/home/[[:alnum:]_]+_gmail_com[^[:space:]"`<>)]*|[[:alnum:]._%+-]+@idolum\.ai|[[:alnum:]_-]+:[[:alnum:]._%+-]+@|client=[[:alnum:]_-]+|account=[[:alnum:]_-]+|[[:alnum:]_-]*telegram-child-bot-subsystem|Organic[[:space:]]+R[[:alpha:]]+|family-group'

if rg -n --glob '*.md' --glob '*.toml' "$private_pattern" "${public_surfaces[@]}" >/dev/null; then
  echo "public docs/config contain private or live-operation markers:" >&2
  rg -n --glob '*.md' --glob '*.toml' "$private_pattern" "${public_surfaces[@]}" >&2
  exit 1
fi

source_private_pattern='[[:alnum:]_]+_gmail_com|/home/[[:alnum:]_]+_gmail_com[^[:space:]"`<>)]*|[[:alnum:]._%+-]+@idolum\.ai|Organic[[:space:]]+R[[:alpha:]]+'

tracked_public_source_files() {
  git ls-files -z | while IFS= read -r -d '' file; do
    case "$file" in
      third_party/*) ;;
      *) printf '%s\0' "$file" ;;
    esac
  done
}

if tracked_public_source_files | xargs -0 -r rg -n "$source_private_pattern" >/dev/null; then
  echo "tracked source contains private workstation or account markers:" >&2
  tracked_public_source_files | xargs -0 -r rg -n "$source_private_pattern" >&2
  exit 1
fi

echo "public readiness check passed"
