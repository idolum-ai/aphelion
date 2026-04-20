# runtime

`runtime` is Aphelion's long-lived house shell.

## Live Ownership

`runtime` owns:

- Telegram ingress and outbound adapter wiring
- principal resolution, scope resolution, and session locking
- pre-turn shell handoff into species assemblers (`interactive_dm`, `maintenance`, and durable-group adapters)
- background loops (heartbeat, cron, startup recovery, idle expiry)
- durable-agent lifecycle wiring
- assembly of concrete governor/face/persistence/delivery ports for `turn`

## Boundaries

`runtime` is not the main owner of one-turn stage order. Turn sequencing runs
through `turn.Machine`, while conversational mechanics are delegated to
`pipeline`.

Authoritative split for interactive DM turns:

- runtime shell (`turn.go`) owns principal admission, scope resolution, chat
  action loops, session locks, and transport event awareness.
- interactive DM species assembly (`interactive_dm_turn.go`) owns one-turn
  construction: shared interactive-like assembly, coordinator/port wiring, and
  `turn.Machine` invocation.
- `turn` owns stage order once invoked.

Authoritative split for maintenance turns (heartbeat/cron/recovery):

- runtime maintenance loops (`heartbeat.go`, `cron.go`, `recovery.go`) own
  maintenance-specific request synthesis, hidden-input gathering, and post-turn
  fanout behavior.
- maintenance species assembly (`maintenance_turn_assembly.go`) owns one-turn
  construction for the maintenance family: coordinator/port wiring and
  `turn.Machine` invocation.
- `turn` owns stage order once invoked.

## Package Map

- `runtime.go`: runtime construction, loops, and process wiring
- `interactive_like_assembly.go`: shared interactive-like turn assembly spine used by DM and durable-group turns
- `interactive_dm_turn.go`: interactive DM species assembler boundary and one-turn construction
- `maintenance_turn_assembly.go`: maintenance execution-family assembly boundary for heartbeat/cron/recovery turns
- `turn*.go`, `durable_group.go`, `maintenance_turn.go`: adapters from runtime facts into `turn`
- `turn_coordinator_common.go`, `turn_coordinator_interactive.go`, `turn_coordinator_durable.go`: shared and species-specific coordinator adapters
- `durable_wake.go`: pluggable durable wake ingress adapters and shared wake-turn substrate
- `durable_*.go`: durable-agent channel runtimes and channel adapters
- `*_runtime_test.go`: runtime-domain integration suites (by concern)
