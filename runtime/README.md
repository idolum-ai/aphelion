# runtime

`runtime` is Aphelion's long-lived house shell.

## Live Ownership

`runtime` owns:

- Telegram ingress and outbound adapter wiring
- principal resolution, scope resolution, and session locking
- background loops (heartbeat, cron, startup recovery, idle expiry)
- durable-agent lifecycle wiring
- assembly of concrete governor/face/persistence/delivery ports for `turn`

## Boundaries

`runtime` is not the main owner of one-turn stage order. Turn sequencing runs
through `turn.Machine`, while conversational mechanics are delegated to
`pipeline`.

## Package Map

- `runtime.go`: runtime construction, loops, and process wiring
- `interactive_like_assembly.go`: shared interactive-like turn assembly spine used by DM and durable-group turns
- `turn*.go`, `durable_group.go`, `maintenance_turn.go`: adapters from runtime facts into `turn`
- `turn_coordinator_common.go`, `turn_coordinator_interactive.go`, `turn_coordinator_durable.go`: shared and species-specific coordinator adapters
- `durable_*.go`: durable-agent channel runtimes
- `*_runtime_test.go`: runtime-domain integration suites (by concern)
