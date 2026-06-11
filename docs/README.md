# Aphelion Docs

These docs are organized by the path you are on. The guides teach workflows; the
architecture and requirements docs define the deeper contracts.

## Choose A Path

| Goal | Start here |
|---|---|
| Try Aphelion safely with a real Telegram bot | [Quick Experiment](guides/quick-experiment.md) |
| Set up a durable local service | [Operator Setup](guides/operator-setup.md) |
| Give isolated work narrow internet access | [Sandbox Networking](guides/sandbox-networking.md) |
| Set up durable child agents | [Durable Children](guides/durable-children.md) |
| Learn the Telegram control surface | [Telegram Operations](guides/telegram-operations.md) |
| Keep parallel work separated | [Telegram Operations: side threads](guides/telegram-operations.md#keep-parallel-requests-apart) |
| Inspect context, memory, missions, and model routing | [Telegram Operations: work surfaces](guides/telegram-operations.md#manage-work-surfaces) |
| Understand or contribute to the codebase | [Contributor Handbook](guides/contributor-handbook.md) |
| Plan and review a release | [Release Strategy](guides/release-strategy.md) |

## References

- [Telegram UI Features](telegram-ui-features.md): canonical command and button
  reference.
- [Promise Ledger](promises.md): current public promises and implementation
  status.
- [Architecture Map](architecture/README.md): package shape, truth surfaces, and
  canonical diagrams.
- [Design Principles](architecture/design-principles.md): normative direction.
- [Requirements Index](../requirements/INDEX.md): component behavior specs.
- [Public Release Provenance](public-release.md): canonical source and private-history boundary.
- [Release Strategy](guides/release-strategy.md): release branch, review, notes, and automation plan.

## Reading Order

New operators should read the quick experiment guide first, then the Telegram
operations guide once the bot is running.

Operators maintaining a live service should read the operator setup guide and
keep the Telegram reference nearby. Read sandbox networking only when a
non-admin or durable profile needs explicit internet egress.

Contributors should read the contributor handbook, then the design principles,
package ownership map, and any requirements doc for the behavior they are
changing. Prompt or agency changes should also read the
[agency evaluation methodology](architecture/agency-evaluation-methodology.md)
before updating prompt contracts or eval expectations.
- [Aphelion, in boring words](architecture/aphelion-in-boring-words.md) — plain-English map from Aphelion terminology to systems/security concepts.
