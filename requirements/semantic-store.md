# Semantic Store — Local Semantic Memory Substrate, Import, and Retrieval Modes

## Overview

Aphelion should own a local semantic memory substrate.

This substrate is not the same thing as:

- constitutional prompt files
- curated memory prose files
- session transcript ledgers
- external vector stores

It is the local retrieval layer that supports semantic search over approved corpora while keeping canonical truth local and operator-owned.

The semantic store belongs in Aphelion's codebase because it defines:

- how semantic chunks are stored
- how vectors are versioned
- how provenance survives migration
- how imported memory becomes Aphelion-owned without becoming originless

The open-source boundary is:

- **Aphelion owns the substrate**
- **operators own the corpus**

In other words:

- schema, retrieval semantics, importers, and migrations are publishable system design
- operator, household, and configured-face memory content is local private data

## Goals

- define an Aphelion-owned canonical local semantic store
- keep semantic retrieval subordinate to local constitutional truth
- support import from older foreign stores such as OpenClaw/Host
- preserve provenance during import
- distinguish live-turn retrieval from heartbeat/reflection retrieval
- avoid permanent dependency on foreign runtimes or foreign schemas
- allow future acceleration without making acceleration the core contract

The attribution and departure record for the OpenClaw/Host comparison lives in
[`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md).

## Non-Goals

- making OpenAI or any external vector store the canonical semantic backend
- silently injecting semantic hits into every turn
- conflating transcript recall with semantic memory retrieval
- embedding private corpora into the open-source repo
- requiring sqlite-vec or any one extension for correctness

## Ownership Boundary

### Aphelion-owned substrate

The codebase should define:

- local semantic store schema
- schema versioning and migrations
- canonical chunk model
- provenance model
- source-kind model
- retrieval result shape
- importer interfaces
- retrieval modes
- ranking composition rules
- fallback search behavior when vectors are absent
- optional acceleration hooks

### Operator-owned corpus

The operator retains ownership of:

- private memory files
- imported archived corpora
- family facts and personal archives
- legal material
- creative work
- local source paths
- credentials and external storage decisions

The substrate should work for any operator corpus, not just the maintainer's.

## Relationship to Memory Layers

The semantic store is a retrieval aid over approved local corpora.

It does not outrank:

- constitutional files
- curated memory files as canonical text
- the session ledger as the record of what actually happened

Hierarchy remains:

- constitutional files = identity and authority
- curated memory files = durable claims and narrative
- session ledger = transcript truth
- semantic store = retrieval substrate over approved content

## Canonical Local Store

Aphelion should prefer a local SQLite semantic store as the canonical substrate.

Reasons:

- local and inspectable
- operator-portable
- easy to snapshot and migrate
- good enough for small-to-medium private corpora
- works with optional FTS and vector acceleration

The canonical store should remain valid even without vector extensions.

That means:

- correctness must not depend on sqlite-vec
- approximate or accelerated search may be optional
- in-process cosine fallback should remain possible

## Data Model

The exact table names may evolve, but the model should stay stable.

### Documents

A document represents one semantic source file or imported source artifact.

Suggested fields:

- `id`
- `scope` (`shared`, `principal`)
- `principal_id` (required when scope is `principal`; identifies the specific principal, not just the scope class; must not be empty for principal-scoped documents; should use the stable principal key rather than a display label)
- `source_path`
- `source_kind` (`memory`, `knowledge`, `decision`, `daily_note`, `question`, `rhizome`, `imported`, etc.)
- `source_class` (`curated`, `daily_note`, `archive`, `imported_archive`)
- `provenance_source` (`native`, `host_archive`, `openclaw_import`, etc.)
- `import_state` (`quarantine`, `approved`, `rejected`) — imported documents must not be eligible for live-turn retrieval until explicitly approved; only native documents may default to `approved`
- `checksum`
- `mtime`
- `created_at`
- `updated_at`
- `metadata_json` (optional, bounded)

### Chunks

A chunk represents one retrievable semantic unit inside a document.

Suggested fields:

- `id`
- `document_id`
- `ordinal`
- `text`
- `text_hash`
- `start_line` / `end_line` when meaningful
- `start_offset` / `end_offset` when line-based offsets are unavailable
- `embedding_model`
- `embedding_dims`
- `embedding_json` or equivalent local vector payload
- `created_at`
- `updated_at`

### Optional lexical projection

If FTS is enabled, the store may project chunks into an FTS table.

That projection is derived state, not the canonical record.

### Optional acceleration projection

If sqlite-vec or another acceleration layer is available, the vector index should be treated as a projection over canonical chunks, not the deepest truth.

The canonical retrieval index state remains:

- document rows
- chunk rows
- vector payloads

These are the canonical records of the retrieval substrate, not the canonical source of memory truth. The source files from which chunks were derived remain the canonical memory truth. The semantic store reflects those files; it does not replace them.

## Source Kinds

Source kinds belong in the substrate because retrieval quality and authority depend on them.

Suggested first-wave kinds:

- `memory`
- `knowledge`
- `decision`
- `daily_note`
- `question`
- `rhizome`
- `imported_archive`

These kinds should be visible in retrieval results and available for ranking policy.

## Provenance

Provenance is mandatory.

Imported semantic memory must not become originless.

At minimum, imported chunks should retain:

- import source (`host_archive`, `openclaw_import`)
- original source path
- original embedding model when vectors were imported
- import timestamp

This preserves the ability to say:

- this hit came from Aphelion-native curated memory
- this hit came from Host's old archive
- this hit was imported rather than authored in the new harness

That does not keep Host separate as a sovereign layer. It preserves memory discipline.

## Chunking Policy

Chunking should be source-aware.

A single fixed character window is not enough.

Recommended first-wave policy:

- `MEMORY.md` → chunk by section
- `memory/knowledge.md` → chunk by subsection, then split large bullet clusters
- `memory/decisions.md` → one chunk per decision entry
- `memory/questions.md` → chunk by open-question cluster
- `memory/rhizome.md` → chunk by thematic block, not by individual link line
- daily notes → chunk by dated sub-entry or named section

Goals:

- keep chunks semantically coherent
- avoid burying one durable fact inside a giant blob
- avoid over-fragmenting tightly related bullet clusters

## Retrieval Modes

One substrate should support two modes.

### 1. Interactive retrieval

Used during live turns.

Interactive semantic retrieval should behave like governed recall, not ambient memory.

That means:

- retrieval remains deliberate by default during ordinary interactive turns
- semantic hits are bounded clues, not recovered transcript truth
- results must not silently merge into the default prompt world as if they were already established facts
- the governor should decide when semantic search is worth the latency and ambiguity cost

Needs:

- fast retrieval
- tight chunking
- bounded `top_k`
- smaller response budgets
- clear result formatting
- explicit governor invocation via tool

### 2. Maintenance retrieval

Used by heartbeat/reflection.

Maintenance retrieval may be automatic when the maintenance function itself requires semantic context.

This is different from live-turn retrieval because:

- the runtime is already performing a maintenance-owned query rather than ordinary conversation
- broader thematic context is often the point of the maintenance pass
- recurrence, contradiction, and clustering signals are useful even before the governor explicitly asks for them

Needs:

- broader thematic recall
- contradiction detection support
- recurrence support
- larger response budgets
- slower cadence
- clustering-friendly behavior

These are two query modes over one substrate, not two separate systems.

## Ranking

Initial ranking may be simple, but the substrate should allow composition.

Useful components:

- lexical similarity
- vector similarity
- source-kind prior
- recency prior for daily notes when explicitly enabled
- provenance-aware weighting if needed

Recommended authority tendency:

- `knowledge` > `decision` > `memory` > `daily_note` > `question` > `rhizome`

This should be a retrieval heuristic, not a truth override.

Ranking should influence presentation and governor attention, not convert semantic proximity into canonical truth.


## Retrieval Result Shape

Semantic hits should be bounded and explicit.

Each hit should carry at least:

- source file or corpus
- scope (`shared` or `principal`)
- principal discriminator when scope is `principal` — use the stable principal key, not a display label; `scope = principal` alone is not sufficient to audit which principal corpus a hit came from when searches span multiple principals
- score
- excerpt
- source-kind label such as `memory`, `knowledge`, `decision`, `daily_note`, `question`, `rhizome`
- provenance label such as `native`, `host_archive`, `openclaw_import` when available

Semantic hits should be presented as retrieved semantic context, not disguised as constitutional truth, transcript truth, or ordinary user messages.

The governor-facing shape should communicate epistemic status clearly:

- this was retrieved because it appears semantically nearby
- this may help orient judgment
- this does not become established fact merely by being retrieved

In live turns, retrieval context should feel like bounded clues under review, not hidden memory bleed.

## Fallback Behavior

Aphelion should remain usable when vector search is unavailable.

Fallback path may use:

- FTS
- lexical scoring
- in-process cosine over locally stored vectors

The system should degrade, not disappear.

If imported embeddings are preserved before vector search is wired into live retrieval, they should be treated as preserved metadata rather than silently active ranking inputs. Operator-facing import output should make that explicit.

## Importers

Import is a first-class requirement.

Aphelion should support importers for foreign semantic stores.

### OpenClaw / Host importer

The first required importer should support the OpenClaw memory store shape used by Host.

Known properties of that store:

- SQLite-backed
- files/chunks schema
- chunk text stored directly
- embeddings stored as JSON arrays
- source/path preserved
- optional sqlite-vec acceleration

The importer should:

- read source documents and chunks
- preserve provenance
- import vectors when the source model and shape match
- record the original embedding model alongside each imported chunk
- when re-embedding is needed, create new chunks with a new model tag rather than silently overwriting; this keeps ranking behavior auditable across embedding model changes
- avoid permanent runtime dependency on the foreign DB

The first implementation may target an explicitly observed OpenClaw / Host schema contract rather than pretending to support every schema variation.
That narrow import stance is part of the departure recorded in
[`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md).

If Aphelion only supports one observed foreign layout, it should say so plainly in code, logs, and operator output, for example as an import contract label such as `openclaw_observed_v1`.

Unknown or mismatched foreign layouts should fail closed with an explicit schema-contract error rather than being imported optimistically.

### Import rule

Import should produce Aphelion-owned local semantic state.

It should not leave Aphelion permanently querying the foreign runtime as its main semantic backend.

### Quarantine boundary

Imported documents must enter in `import_state = quarantine`.

Documents in `quarantine` are not eligible for live-turn interactive retrieval.

Documents in `quarantine` are also not eligible for ordinary maintenance or heartbeat retrieval.

The only maintenance operation that may read quarantined material is an explicit import-audit pass — a dedicated maintenance submode whose purpose is to review the quarantined corpus. The preferred first implementation is a CLI maintenance path such as `aphelion import-audit`, because that keeps quarantine review outside ordinary conversational flow and reduces accidental approval. It may later gain other operator-controlled entry points, but it should run as maintenance rather than as an ordinary interactive tool call. Ordinary heartbeat and reflection must not pull quarantined material, because maintenance retrieval feeds the reflection loop, and reflection can write to curated memory. Allowing quarantined content to reach reflection indirectly defeats the quarantine boundary.

The CLI import-audit flow should be conservative by default:

- default to read-only listing and review
- show provenance, scope, principal key when applicable, source path, and bounded excerpts
- require explicit operator selection for approve/reject decisions
- record review and promotion decisions in durable local state

Promotion to `approved` requires an explicit operator action.

The governor may surface candidates, observations, or review notes about quarantined material, but the final promotion decision belongs to the operator. Corpus admission is an operator-owned decision, not a model-owned one.

This is the boundary between "this has been imported" and "this has been approved for retrieval."

The first implementation should also keep state transitions narrow:

- approval/rejection should apply only to quarantined imported archives
- native documents must never flow through the import-audit state machine
- already approved or rejected imports should not be silently re-transitioned by the same approval path

The distinction matters because imported corpora may contain stale facts, private material from the source system, or content that should not surface in any retrieval path until reviewed.

## Tool Invocation vs Preloaded Context

The boundary between interactive tool use and maintenance preload should stay explicit.

### Interactive turns

During ordinary interactive work, semantic retrieval should normally arrive through an explicit governor action such as `semantic_search`.

This keeps retrieval:

- visible in the turn log
- scoped to the current question
- clearly separate from the prompt's constitutional and curated-memory baseline

### Maintenance turns

During heartbeat, reflection, and other maintenance-owned passes, the runtime may preload semantic context when that context is part of the maintenance function itself.

This is allowed because the system is already doing maintenance interpretation rather than live user-facing judgment.

Even then, preloaded semantic context should remain labeled as retrieval output rather than flattened into constitutional or curated truth.

## External Stores

OpenAI files/vector stores may still be useful, but remain auxiliary.

They may be used for:

- corpus backup
- later attachment to external retrieval services
- experiments
- portability to external tooling

They must not replace the canonical local semantic substrate.

## Open-Source Boundary

Safe to publish:

- schema design
- retrieval modes
- importer interfaces
- migration logic
- ranking policy
- fallback logic
- config surface
- tests

Should remain local:

- operator memory corpus
- imported private embeddings
- family facts
- legal archives
- operator-specific corpora

## Config Surface

See `config.md`, but the substrate should be reflected in config as:

- enable/disable semantic subsystem
- local backend selection
- indexed source classes
- retrieval limits by mode
- optional acceleration settings
- importer/refresh policy

## Decisions

- **Aphelion owns the semantic substrate.** This belongs in the codebase.
- **The corpus remains operator-owned.** Private memory is not part of the public substrate.
- **One substrate, two retrieval modes.** Interactive and maintenance are distinct query behaviors over one store.
- **Provenance survives import.** Imported memory must not become originless.
- **Correctness must not depend on sqlite-vec.** Acceleration is optional.
- **Foreign stores are import sources, not permanent sovereign backends.**
- **External vector stores remain auxiliary.** They do not replace local semantic truth.

## Test Plan

- **TestSemanticStoreSchemaVersioned**: local semantic store exposes schema version metadata
- **TestSemanticStorePersistsDocumentsAndChunks**: imported/native documents and chunks survive restart
- **TestSemanticStorePreservesProvenance**: imported Host chunks retain import source + original path
- **TestSemanticSearchInteractiveReturnsBoundedHits**: live-turn retrieval respects interactive limits
- **TestSemanticSearchHeartbeatUsesMaintenanceLimits**: heartbeat retrieval uses broader limits
- **TestSemanticFallbackWithoutVectorExtension**: semantic retrieval still works without sqlite-vec
- **TestOpenClawImporterReadsChunkTextAndEmbeddings**: Host/OpenClaw importer maps old rows correctly
- **TestImportedHitsLabelSourceKindAndProvenance**: retrieval results expose both kind and provenance
- **TestForeignStoreNotRequiredAfterImport**: imported Aphelion store remains usable after removing the source archive
