# Architecture Waivers

This file is the single ledger for temporary architecture waivers.

Rules:
- Every waiver must include `owner` and `expires_on`.
- Expired waivers must be removed or renewed in an explicit follow-up commit.
- Waivers are temporary seams, not permanent abstractions.

## Active Waivers

### `pipeline-compat-should-render-idolum-reply`
- `status`: active
- `scope`: `pipeline/contracts.go` (`ShouldRenderIdolumReply`)
- `owner`: runtime/turn refactor track
- `expires_on`: 2026-07-31
- `reason`: Backward-compatible wrapper while all callsites rely on `ShouldRenderInteractiveIdolumReply` directly.
- `exit_criteria`: Remove the wrapper once no production callsite requires it.
