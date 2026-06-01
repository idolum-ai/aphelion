#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/generate-release-notes.sh [--from REF] [--to REF] [--output PATH] [--title TEXT]

Generate draft release notes from a git commit range. When --from is omitted,
the script uses the most recent reachable tag before --to, falling back to the
repository root commit when no prior tag exists.
USAGE
}

from_ref=""
to_ref="HEAD"
output_path=""
title="Release notes draft"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --from)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      from_ref="$2"
      shift 2
      ;;
    --to)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      to_ref="$2"
      shift 2
      ;;
    --output)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      output_path="$2"
      shift 2
      ;;
    --title)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      title="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}

need_cmd git
need_cmd date

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

to_commit="$(git rev-parse --verify "${to_ref}^{commit}")"
if [[ -z "${from_ref}" ]]; then
  if git rev-parse --verify "${to_commit}^" >/dev/null 2>&1; then
    from_ref="$(git describe --tags --abbrev=0 "${to_commit}^" 2>/dev/null || true)"
  fi
  if [[ -z "${from_ref}" ]]; then
    from_ref="$(git rev-list --max-parents=0 "${to_commit}" | tail -n1)"
  fi
fi
from_commit="$(git rev-parse --verify "${from_ref}^{commit}")"
range="${from_commit}..${to_commit}"
short_from="$(git rev-parse --short "${from_commit}")"
short_to="$(git rev-parse --short "${to_commit}")"
generated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

emit_notes() {
  cat <<NOTES
# ${title}

Generated: ${generated_at}
Range: \`${short_from}..${short_to}\`

## Summary

- Draft release candidate notes generated from git history.
- Review and edit before publishing a GitHub release.

## Changes

NOTES

  mapfile -t commit_lines < <(git log --first-parent --reverse --format='%s%x09%h' "${range}")
  if [[ "${#commit_lines[@]}" -gt 0 ]]; then
    for line in "${commit_lines[@]}"; do
      IFS=$'\t' read -r subject short_hash <<<"${line}"
      subject="$(printf '%s' "${subject}" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')"
      [[ -n "${subject}" ]] || continue
      printf -- '- %s (`%s`)\n' "${subject}" "${short_hash}"
    done
  else
    printf '_No commits found in this range._\n'
  fi

  cat <<'NOTES'

## Release checklist

- [ ] Release-candidate branch validation passed.
- [ ] Release notes were reviewed for operator-facing clarity.
- [ ] Public release boundary still holds: no private configs, logs, credentials, or runtime artifacts.
- [ ] Tag/release publication has separate approval.
- [ ] Service deploy/restart, if any, has separate approval.
NOTES
}

if [[ -n "${output_path}" ]]; then
  mkdir -p "$(dirname "${output_path}")"
  emit_notes > "${output_path}"
  echo "wrote ${output_path}"
else
  emit_notes
fi
