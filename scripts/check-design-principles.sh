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
  "Prefer one-hop trace affordances over scattered forensic scavenging"
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
  "None."
  "## Machine-Checked Paths"
)

for phrase in "${required_debt_terms[@]}"; do
  if ! rg -qF "$phrase" "$debt_doc"; then
    echo "principle debt ledger missing required phrase: $phrase" >&2
    exit 1
  fi
done

for phrase in "I need to correct that" "Sending Work evidence" "Operator card:" "Use the buttons" "Approval needed." "Lease class:"; do
  if rg -nF "$phrase" runtime session core durableagent tool telegram --glob '!**/*_test.go' >/dev/null; then
    echo "runtime source contains forbidden magic operator phrase: $phrase" >&2
    rg -nF "$phrase" runtime session core durableagent tool telegram --glob '!**/*_test.go' >&2
    exit 1
  fi
done

for symbol in "positiveAuthorityEffectText" "bounded_effect_positive_clause" "operationPhaseApprovalText" "inferOperationGateReasonCode" "operationPhaseIsEscalatedOperatorApproval" "detectExecutionClaims" "textRequestsPendingAudioTranscription" "textRequestsAudioTranscription" "lexical_safety_scanner" "status_line_fallback" "goalContinuationSourceIndicatesCompletion"; do
	if rg -nF "$symbol" runtime session --glob '!**/*_test.go' >/dev/null; then
		echo "runtime source contains retired prose-authority classifier: $symbol" >&2
		rg -nF "$symbol" runtime session --glob '!**/*_test.go' >&2
		exit 1
	fi
done

if rg -nF "EXTERNAL_CHANNEL_STATUS" runtime session core durableagent tool telegram --glob '!**/*_test.go' >/dev/null; then
  echo "runtime source contains retired external-channel status-line fallback" >&2
  rg -nF "EXTERNAL_CHANNEL_STATUS" runtime session core durableagent tool telegram --glob '!**/*_test.go' >&2
  exit 1
fi

if ! rg -qF "EXTERNAL_CHANNEL_OUTCOME" runtime/external_channel_wake.go; then
  echo "external channel wakes must request a typed wake outcome contract" >&2
  exit 1
fi

if ! rg -qF "interpretCurrentTurnClaims" runtime/interpretation_claims.go || ! rg -qF "INTERPRETATION_CLAIMS" runtime/interpretation_claims.go; then
  echo "runtime must expose a typed model interpretation claim lane" >&2
  exit 1
fi

if ! rg -qF "interpretFinalReplyExecutionClaims" runtime/constitution_runtime.go; then
	echo "final-reply grounding must use typed interpretation claims before TES validation" >&2
	exit 1
fi

if ! rg -qF "interpretGoalContinuationClaims" runtime/goal_continuation.go || ! rg -qF "goal_continuation" runtime/goal_continuation.go; then
	echo "goal-continuation follow-up inference must use typed interpretation claims" >&2
	exit 1
fi

if rg -n "msg\\.Text|normalizeMediaIntentText|containsTranscriptionTerm|containsAudioTerm" runtime/media_intent.go >/dev/null; then
	echo "media intent routing must not inspect authored text directly" >&2
  rg -n "msg\\.Text|normalizeMediaIntentText|containsTranscriptionTerm|containsAudioTerm" runtime/media_intent.go >&2
  exit 1
fi

if ! rg -qF "writeDoctorDesignPrincipleHealth" runtime --glob 'doctor*.go'; then
  echo "/health diagnose must surface design-principle health" >&2
  exit 1
fi

for symbol in "OperatorTitle" "PlanTitle"; do
  if ! rg -qF "$symbol" session --glob 'types*.go'; then
    echo "session state must carry explicit operator/plan title fields: $symbol" >&2
    exit 1
  fi
done

if ! rg -qF '"interpretation_claims"' runtime/constitution_runtime.go; then
  echo "final-reply claim adjudication must persist typed interpretation claims" >&2
  exit 1
fi

if ! rg -qF 'proposal.OperatorTitle = ""' runtime/continuation_lease.go || ! rg -qF 'proposal.PlanTitle = ""' runtime/continuation_lease.go; then
  echo "action proposal hashes must ignore presentation title fields" >&2
  exit 1
fi

if ! rg -qF "NextRepairAction" runtime --glob '!**/*_test.go' || ! rg -qF "DebugBreadcrumb" core/status.go || ! rg -qF "attachPendingItemDebugBreadcrumbs" runtime --glob '!**/*_test.go' || ! rg -qF "next_repair_action" face --glob '!**/*_test.go'; then
  echo "operator debug breadcrumbs must cover review events and status pending items" >&2
  exit 1
fi

echo "design principles check passed"
