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

Dense records are still allowed when clearly labeled as debug or evidence:
`/debug`, logs, machine-readable Tailnet/status mirrors, forensic records, and
explicit evidence sections may expose raw identifiers or enum-heavy state.

The goal is to keep the outpost legible without turning Aphelion into a broader
dashboard or platform. Operator control remains Telegram and CLI.
