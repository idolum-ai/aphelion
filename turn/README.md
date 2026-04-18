# turn

`turn` is the production owner of Aphelion's one-turn state machine.

## Live Ownership

`turn.Machine` owns:

- stage ordering for one scoped turn
- policy application by run kind/species
- governor and face orchestration through explicit ports
- commit-order contracts between persistence and delivery

`turn` is used by runtime for:

- interactive DM turns
- durable Telegram child turns
- heartbeat turns
- cron turns
- startup recovery turns

## Boundaries

`turn` does not own:

- transport polling/sending adapters
- session lock lifecycles
- long-lived background loops
- provider/bootstrap shell wiring

Those concerns remain in `runtime`.

`turn` also does not own low-level governor/face conversational transforms
(brokerage parsing, floor materialization, render-decision helpers). Those
remain in `pipeline`.

For interactive DM species specifically, runtime now uses an explicit assembler
boundary before `turn.Machine` execution:

- runtime shell resolves principal/scope/session lock + transport context
- runtime species assembler constructs coordinator/ports and invokes `turn`
- `turn` applies stage order and commit/delivery orchestration

## Package Map

- `engine.go`, `stages.go`: state machine orchestration
- `brokerage_stage.go`: bounded brokerage convergence orchestration helper
- `render_stage.go`: render-stage stream/non-stream/fallback selection helper
- `delivery_stage.go`: delivery/record/post-commit ordering helper
- `persist_stage.go`: persist ordering helper (convert/apply sidecars/load state/save)
- `policy.go`: species/policy defaults
- `ports.go`: governor/face/persistence/delivery interfaces
- `commit.go`: commit and delivery request/result contracts
- `awareness.go`: turn-level awareness shaping helpers

## Contract Goal

The package should remain the orchestration spine. If code is primarily about
transport, process supervision, or provider wire behavior, it belongs outside
`turn`.
