# Architecture Waivers

This file is the single ledger for temporary architecture waivers.

Rules:
- Every waiver must include `owner` and `expires_on`.
- Expired waivers must be removed or renewed in an explicit follow-up commit.
- Waivers are temporary seams, not permanent abstractions.

## Active Waivers

None.

## Resolved Waivers

### `pipeline-compat-should-render-idolum-reply`
- `status`: resolved on 2026-04-16
- `scope`: `pipeline/contracts.go` (`ShouldRenderIdolumReply`)
- `owner`: runtime/turn refactor track
- `reason`: compatibility wrapper was removed after all in-repo callsites were using `ShouldRenderInteractiveIdolumReply`.

### `face-fallback-compat-wrapper`
- `status`: resolved on 2026-04-16
- `scope`: `face/fallback.go` (`SerializeFloorFallback`, `FallbackOptions` alias)
- `owner`: runtime/turn refactor track
- `reason`: wrapper duplicated `pipeline` ownership and had no production callsites; removed with duplicate face fallback tests.
