# pipeline

`pipeline` is the production owner of governor/face conversational mechanics.

## Live Ownership

This package currently owns:

- brokerage parsing and ratification shaping
- floor material parsing and floor formatting helpers
- floor fallback serialization helpers
- visible-reply constitution validation and repair-note shaping
- execution/render contract structs shared by runtime and turn
- interactive render-decision policy helpers

It is invoked by `runtime` and `turn` orchestration paths, and is covered by
dedicated tests under `pipeline/*_test.go`.

## Boundaries

`pipeline` should stay focused on conversational transformations. It should not
absorb:

- process-shell wiring
- session lock/load behavior
- transport delivery semantics
- background loop scheduling

Those remain in `runtime`.

`pipeline` also should not own turn stage ordering; that remains in `turn`.

## Package Map

- `contracts.go`, `types.go`: boundary contracts and policy helpers
- `material.go`: floor extraction and formatting
- `fallback.go`: material-floor fallback serialization
- `constitution.go`: visible-reply constitution checks and repair-note shaping
- `brokerage.go`: brokerage parsing and ratification parsing

## Contract Goal

If code answers “how does bounded governor/face material transform,” it belongs
here. If it answers “when/how does a turn progress through stages,” it belongs
in `turn`.
