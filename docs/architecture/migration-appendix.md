# Migration Appendix

![Present vs intended](diagrams/07-present-vs-intended.svg)

The turn/pipeline ownership migration has landed in production code. This
appendix keeps the comparison artifact for historical and review context.

Primary closeout reference:

- [`requirements/turn-pipeline-refactor.md`](../../requirements/turn-pipeline-refactor.md)

Practical reading:

- `runtime` acts as house shell and adapter wiring.
- `turn` owns stage order and commit semantics.
- `pipeline` owns conversational transforms.

Legacy render variants are kept in [`diagrams/archive/`](diagrams/archive).

