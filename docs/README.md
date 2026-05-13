# Aphelion Docs

These docs are organized by the path you are on. The guides teach workflows; the
architecture and requirements docs define the deeper contracts.

## Choose A Path

| Goal | Start here |
|---|---|
| Try Aphelion safely with a real Telegram bot | [Quick Experiment](guides/quick-experiment.md) |
| Set up a durable local service | [Operator Setup](guides/operator-setup.md) |
| Learn the Telegram control surface | [Telegram Operations](guides/telegram-operations.md) |
| Understand or contribute to the codebase | [Contributor Handbook](guides/contributor-handbook.md) |

## References

- [Telegram UI Features](telegram-ui-features.md): canonical command and button
  reference.
- [Promise Ledger](promises.md): current public promises and implementation
  status.
- [Architecture Map](architecture/README.md): package shape, truth surfaces, and
  canonical diagrams.
- [Design Principles](architecture/design-principles.md): normative direction.
- [Requirements Index](../requirements/INDEX.md): component behavior specs.

## Reading Order

New operators should read the quick experiment guide first, then the Telegram
operations guide once the bot is running.

Operators maintaining a live service should read the operator setup guide and
keep the Telegram reference nearby.

Contributors should read the contributor handbook, then the design principles,
package ownership map, and any requirements doc for the behavior they are
changing.
