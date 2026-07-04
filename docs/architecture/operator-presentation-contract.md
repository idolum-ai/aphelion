# Operator Presentation Contract

Aphelion has two supported human operator surfaces: Telegram and CLI. Both use
the same presentation contract for default human panels:

- title
- current status
- why the state matters
- next action
- details and labeled evidence

This is a presentation rule, not an authority rule. Text and buttons render typed
ledger state; they do not grant authority. Grants, leases, consent, continuation
state, child policy, Tailnet registry state, and TES remain the source of truth.

Approval surfaces may expose metered discovery controls such as `Next grant`
when a long-running plan can safely look ahead to the next authority collision.
Those controls spend typed non-executing authority only: they may compile and
render the next candidate approval, but they may not admit execution authority.
The rendered card must point to the underlying contract, ledger entry, or review
event that owns the action.

Recommendation surfaces may expose attention-radar controls such as resume,
ignore, inspect, or park. Those controls are presentation over typed attention
tracks. A recommendation card should name why the track is live now, which
source owns it, what selecting it will compile, and whether the next step will
need authority. A durable source row without a fresh liveness witness may be
shown in details, but should not be rendered as a current next step.

Goal-pursuit surfaces may expose predictive-interception controls such as
approve next boundary, pause, show evidence, amend criterion, or accept done.
Those controls render a goal acceptance contract and current evidence
projection. Evaluator satisfaction is not operator acceptance; the UI should
keep those states distinct.

Dense records are still allowed when clearly labeled as trace or evidence:
`/health trace`, logs, machine-readable Tailnet/status mirrors, forensic records, and
explicit evidence sections may expose raw identifiers or enum-heavy state.

`/status` and default CLI commands should render operator panels by default.
Stable automation callers must opt into structured output: use `--format=kv` for
key/value script contracts and `--format=json` or `--json` for JSON contracts.

The goal is to keep the outpost legible without turning Aphelion into a broader
dashboard or platform. Operator control remains Telegram and CLI.
