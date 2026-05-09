#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

fail=0

check_max_lines() {
  local file="$1"
  local max_lines="$2"

  if [[ ! -f "$file" ]]; then
    echo "missing structural taste file: $file" >&2
    fail=1
    return
  fi

  local lines
  lines="$(wc -l <"$file" | tr -d ' ')"
  if (( lines > max_lines )); then
    echo "structural taste line cap exceeded: $file has $lines lines, max $max_lines" >&2
    fail=1
  fi
}

# DP-005 debt surfaces. These caps are intentionally generous enough to avoid
# churn, but tight enough to catch growth back into pre-split broad files.
check_max_lines "session/store.go" 1200
check_max_lines "runtime/continuation.go" 400
check_max_lines "runtime/continuation_materialize.go" 500
check_max_lines "runtime/status.go" 1000
check_max_lines "telegram_decisions.go" 2200
check_max_lines "commands.go" 1300
check_max_lines "maintenance_durable_agent.go" 1300
check_max_lines "quickstart.go" 1000

while IFS= read -r file; do
  check_max_lines "$file" 1800
done < <(
  {
    find session -maxdepth 1 -name 'store_*.go' ! -name '*_test.go' -print
    find runtime -maxdepth 1 \( -name 'continuation_*.go' -o -name 'status_*.go' \) ! -name '*_test.go' -print
  } | sort
)

if (( fail != 0 )); then
  echo "structural taste check failed" >&2
  exit 1
fi

echo "structural taste check passed"
