#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

principles_doc="docs/architecture/design-principles.md"
debt_doc="docs/architecture/principle-debt.md"
architecture_index="docs/architecture/README.md"

for file in "$principles_doc" "$debt_doc" "$architecture_index"; do
  if [[ ! -f "$file" ]]; then
    echo "missing design-principle surface: $file" >&2
    exit 1
  fi
done

required_principles=(
  "Text is presentation, not authority"
  "Compile contracts; interpret ambiguity"
  "Short paths to truth"
  "Prefer one-hop debug affordances over scattered forensic scavenging"
)

for phrase in "${required_principles[@]}"; do
  if ! rg -qF "$phrase" "$principles_doc"; then
    echo "design principles doc missing required phrase: $phrase" >&2
    exit 1
  fi
done

if ! rg -qF "principle-debt.md" "$architecture_index"; then
  echo "architecture README must include principle-debt.md in the normative map" >&2
  exit 1
fi

required_debt_terms=(
  "## Entry Contract"
  "## Active Debt"
  "## Machine-Checked Paths"
  "Exit gate"
)

for phrase in "${required_debt_terms[@]}"; do
  if ! rg -qF "$phrase" "$debt_doc"; then
    echo "principle debt ledger missing required phrase: $phrase" >&2
    exit 1
  fi
done

high_risk_paths=(
  "session/authority_contract.go"
  "runtime/constitution_runtime.go"
  "runtime/continuation_materialize.go"
  "runtime/operation_phase_gate.go"
  "runtime/goal_continuation.go"
  "runtime/external_channel_wake.go"
  "runtime/continuation.go"
  "runtime/status.go"
)

high_risk_pattern='strings\.(Contains|HasPrefix|CutPrefix)|regexp\.MustCompile'

for path in "${high_risk_paths[@]}"; do
  if [[ ! -f "$path" ]]; then
    echo "machine-checked principle debt path no longer exists: $path" >&2
    echo "remove it from $debt_doc or update the tracked surface" >&2
    exit 1
  fi
  if rg -q "$high_risk_pattern" "$path" && ! rg -qF "\`$path\`" "$debt_doc"; then
    echo "untracked high-risk string-heavy principle debt: $path" >&2
    echo "add a debt entry with an exit gate to $debt_doc" >&2
    exit 1
  fi
done

for phrase in "I need to correct that" "Sending Work evidence"; do
  if rg -nF "$phrase" runtime session core durableagent tool telegram --glob '!**/*_test.go' >/dev/null; then
    echo "runtime source contains forbidden magic operator phrase: $phrase" >&2
    rg -nF "$phrase" runtime session core durableagent tool telegram --glob '!**/*_test.go' >&2
    exit 1
  fi
done

if ! rg -qF "writeDoctorDesignPrincipleHealth" runtime/doctor.go; then
  echo "/doctor must surface design-principle health" >&2
  exit 1
fi

echo "design principles check passed"
