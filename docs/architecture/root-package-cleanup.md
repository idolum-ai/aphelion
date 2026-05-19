# Root Package Cleanup Plan

Aphelion currently keeps most application orchestration in the repository root as
`package main`. That was useful while the runtime, Telegram surface, maintenance
commands, and deployment tools were changing together, but the root now carries
too much unrelated responsibility.

Current baseline on `main` (`e63d80373d457bc6412cad57499c5055f9935fd0`):

- 183 root Go files, all `package main`
- 62 command files (`commands*`)
- 37 maintenance command files (`maintenance*`)
- 37 Telegram-related files (`telegram_*`, `telegram_command_*`)
- 18 `main_*` subcommand/helper files

This note is the boundary contract for cleanup. It is intentionally written
before moving files.

## Goals

- Keep the root package small enough to understand as process composition.
- Move feature-specific command and maintenance code behind package boundaries.
- Preserve current operator behavior, CLI flags, Telegram callbacks, service
  install behavior, and tests during each phase.
- Avoid import cycles by making dependency direction explicit before extraction.

## Non-goals

- Do not rename the product or public commands as part of this cleanup.
- Do not change service deployment, systemd units, config format, DB schema, or
  Telegram UX while moving files.
- Do not move the binary entrypoint to `cmd/aphelion` until package seams are
  stable.
- Do not combine file movement with behavioral fixes unless a test first proves
  the existing behavior is wrong.

## Dependency direction

Allowed direction:

```text
cmd/aphelion or root main
  -> internal app wiring / command packages
  -> runtime, session, config, core, face, telegram, tool, provider, etc.
```

Package boundaries should point inward toward existing domain packages. Domain
packages such as `runtime`, `session`, `config`, `core`, `face`, and `telegram`
should not import command packages. Command packages should expose narrow entry
functions and small interfaces rather than root-global helpers.

Root package should eventually keep only:

- `main.go` / process startup
- top-level CLI dispatch glue
- compatibility shims while migrations are in flight
- generated or unavoidable build-specific files, if any

## Target packages

### `internal/maintenancecli`

Owns local maintenance/admin CLI subcommands currently in `maintenance*.go`, for
example schema verification, durable-agent maintenance, tailnet inspection,
repair commands, setup/init/paths/park-restart, and memory import/GC commands.

Interface shape:

```go
func Run(ctx context.Context, args []string, deps Deps) (handled bool, err error)
```

Root remains responsible for process exit handling and top-level unknown-command
rendering. The package may import existing domain packages, but existing domain
packages must not import it.

This should be the first extraction target because maintenance commands are
mostly CLI entrypoints around existing packages and have less Telegram callback
state than command handlers.

### `internal/telegramcommands`

Owns Telegram slash-command parsing, panel rendering, callback decoding, and the
small command-router interfaces currently spread across `commands*.go`.

This package should define its own narrow interfaces:

- `Sender`
- `CallbackSender`
- `Router`
- scoped router extensions such as session/status/auto/memory/thread routing

It may depend on `core`, `face`, `session`, and the low-level `telegram` package.
It must not depend on `runtime` directly unless the dependency is hidden behind a
small adapter in the root/control layer.

Extraction should be slower here because `commands*` files share router types,
callback helpers, pagination, and test helpers.

### `internal/telegramcontrol`

Owns adapter binding between the Telegram command interfaces and runtime/session
storage. This is the likely home for files currently named `telegram_command_*`
and some `telegram_*` control files.

Direction:

```text
root main -> telegramcontrol -> telegramcommands/runtime/session
```

`telegramcommands` should not import `telegramcontrol`. This keeps pure command
behavior testable without full runtime dependencies.

### `internal/app` or root composition shims

Owns runtime construction glue only if `main.go` remains too large after CLI and
Telegram extraction. This package should be introduced late, not first, because
it tends to become a new dumping ground if command boundaries are not already
clean.

### `cmd/aphelion`

Final optional phase. Move the binary entrypoint only after the root package is
small and service/build scripts can be updated safely. This phase touches
Makefile, scripts, deployment templates, and release assumptions, so it should
not be bundled with early cleanup.

## Proposed phase order

1. **Maintenance CLI extraction**
   - Move `maintenance*.go` tests and code into `internal/maintenancecli`.
   - Keep a thin root shim calling `maintenancecli.Run`.
   - Validate: `go test ./internal/maintenancecli . ./session ./runtime ./config -count=1`, then `go test ./...` and `go build ./...`.

2. **Small standalone CLI surfaces**
   - Move `quickstart*`, `agency*`, version/deploy/install/default helper
     surfaces if they are independent enough.
   - Keep root shims for command dispatch.
   - Validate each surface with targeted tests plus full test/build.

3. **Telegram command interface extraction**
   - Move command parsing/rendering/callback behavior into
     `internal/telegramcommands`.
   - Move test helpers with the package or create package-local stubs.
   - Keep runtime/control adapters outside this package.
   - Validate callback and scoped-thread/auto tests first, then full test/build.

4. **Telegram control adapter extraction**
   - Move runtime/session-backed command control into `internal/telegramcontrol`.
   - Keep dependency direction from control to command package, not the reverse.
   - Validate Telegram routing, callback ingress, continuation, thread, and
     health-diagnosis tests.

5. **Composition cleanup**
   - If `main.go` and `main_*` helpers remain too large, introduce `internal/app`
     for construction/wiring.
   - Avoid moving process-exit semantics or CLI usage behavior until tests cover
     them.

6. **Optional `cmd/aphelion` move**
   - Move the actual binary entrypoint last.
   - Update Makefile, scripts, systemd templates, and release docs in one
     explicitly approved deploy-aware phase.

## Phase rules

- One extraction family per PR/commit series.
- Prefer move-only commits followed by adapter cleanup commits.
- Keep public command names, callback data, config behavior, DB behavior, and
  service paths unchanged unless explicitly approved.
- Every phase must leave `go test ./...`, `go build ./...`, and `git diff --check`
  clean before commit.
- If an extraction requires a behavioral fix, write or update the failing test
  first and call it out separately from file movement.

## Open design questions

- Should maintenance CLI packages expose a single `Run` or one package per
  command family?
- Should Telegram command rendering live with command parsing, or should panels
  become a separate `internal/telegramui` package later?
- Which root test helpers should become reusable test packages, and which should
  stay package-local to avoid exporting fake APIs?
- How small does root need to be before `cmd/aphelion` is worth the script/deploy
  churn?
