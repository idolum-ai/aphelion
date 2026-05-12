# Memory — Constitution, Curated Memory, Session Recall & External Retrieval

## Overview

Aphelion memory is intentionally layered. It is not a single blob and it is not owned by one backend.

The source-of-truth layers are local and inspectable:

- workspace constitution files
- shared and per-principal memory files
- SQLite session ledgers

External services such as OpenAI files, vector stores, or embedding APIs are auxiliary memory infrastructure. They can extend recall, search, or storage, but they must not silently become the canonical truth of the system.

Memory writes are governor-owned. Idolum may consume memory-derived context for proposal or rendering, but must not commit durable memory on its own.

## Design Goals

- keep the live truth local, inspectable, and operator-editable
- separate durable curated memory from transcript recall
- avoid forcing everything important into `MEMORY.md`
- make cross-session recall explicit and searchable rather than ambient
- allow remote retrieval/storage providers without making them the constitutional core

## Scope

### Required

- workspace bootstrap files
- dynamic memory files
- shared vs per-principal memory roots in the staged design
- SQLite session transcript as the durable conversation ledger

### Next Phase

- isolated per-principal writable memory
- shared memory as read-only for non-admins
- bounded review-event inputs for admin awareness
- explicit session recall/search over the transcript ledger
- a governor-owned `memory` tool for curated memory writes
- structured curated memory files beyond `MEMORY.md`
- source/confidence tagging for stored facts
- scheduled reflection that distills raw notes into curated memory
- decay / archive policy for daily notes and curated memory surfaces

### Deferred

- embeddings-backed semantic indexing
- `file_search`-style retrieval over approved corpora
- multimodal memory indexing
- separate semantic retrieval modes for live turns vs heartbeat/reflection

## Memory Layers

### 1. Global constitution

Examples:

- `SOUL.md`
- `IDENTITY.md`
- `USER.md`
- `AGENTS.md`
- `TOOLS.md`
- `BOOTSTRAP.md`
- `IDOLUM.md`
- `QUESTIONS-TO-IDOLUM.md`

These files shape identity, tone, posture, and policy. They are global/admin-controlled and should be treated as constitutional rather than conversational memory.

`USER.md` should be interpreted as the admin/operator profile, not as a generic shared profile for every future principal.

### 2. Shared curated memory

This is long-lived shared state for the system.

- writable by admin
- read-only for non-admins
- intended to stay compact and durable

Examples:

- `MEMORY.md`
- `memory/knowledge.md`
- `memory/decisions.md`
- `memory/rhizome.md`
- `memory/questions.md`
- admin/maintenance daily notes
- promoted decisions and persistent environment facts

This layer is for facts that should survive beyond one session and be cheaply available in future turns.

### 3. Per-principal curated memory

Per-principal memory is writable only by the corresponding non-admin principal.

Examples:

- isolated notes
- local state snapshots
- private working summaries
- principal-specific preferences or patterns
- principal-scoped profile memory

This is not shared memory. It should not be injected into other principals' turns except through intentional review/digest flows.

### 4. Session transcript ledger

The session ledger is the durable record of what happened in conversation.

It is not the same thing as curated memory.

Use the transcript ledger for:

- what was actually said
- what tools ran
- what Idolum rendered to the user
- what Aphelion canonically decided

Do not stuff general transcript history into `MEMORY.md`. That leads to bloat and loss of structure.

### 5. Session recall / search

Session recall is an explicit retrieval layer over the session ledger.

Use it when the system needs to answer questions like:

- "what did we discuss last week?"
- "what happened in that earlier debugging session?"
- "did this user already mention that preference?"

This layer should be opt-in and query-driven, not blindly injected every turn.

### 6. External retrieval/storage

External services may extend memory behavior, but remain subordinate to local truth.

Examples:

- OpenAI files
- OpenAI vector stores
- embedding-backed search indexes
- later multimodal retrieval corpora

These are useful for corpus search, durable attachments, and semantic recall, but they are not the living constitution of the system.

### 7. Semantic retrieval layer

Semantic retrieval is a query-driven layer over curated memory and other approved corpora.

See also: [`semantic-store.md`](semantic-store.md) for the Aphelion-owned local semantic substrate, provenance-preserving import rules, and retrieval-mode split.

It is not ambient prompt injection.

Use it when the system needs to answer questions like:

- "what else in memory is close to this idea?"
- "does any prior curated note or decision connect to this theme?"
- "what recurring pattern is semantically nearby even if the keywords differ?"

The semantic layer remains subordinate to:

- constitutional files
- curated memory files
- the session ledger

It is a retrieval aid, not a new source of truth.

## Structured Curated Memory

`MEMORY.md` should remain the primary curated memory file, but it should not be the only durable memory surface forever.

Aphelion should support a small set of specialized curated files:

- `MEMORY.md`
  - compact journal-style long-term understanding
- `memory/knowledge.md`
  - structured facts, preferences, relationships, principles, commitments
- `memory/decisions.md`
  - decisions with context, alternatives, rationale, and later follow-ups
- `memory/rhizome.md`
  - lateral associations and non-hierarchical connections
- `memory/questions.md`
  - open uncertainties worth revisiting later

This keeps memory differentiated instead of turning `MEMORY.md` into a junk drawer.

Not all of these files need to be baseline runtime prompt surfaces.

Recommended stance:

- baseline curated files:
  - `MEMORY.md`
  - `memory/knowledge.md`
  - `memory/decisions.md`
- optional advanced practice files:
  - `memory/rhizome.md`
  - `memory/questions.md`

That keeps the core system minimal while leaving room for richer memory practices.

In runtime prompt assembly, the baseline curated files should be treated as the default dynamic curated memory surfaces:

- `MEMORY.md`
- `memory/knowledge.md`
- `memory/decisions.md`

Optional advanced practice files such as `memory/rhizome.md` and `memory/questions.md` should remain on-demand or maintenance-oriented unless explicitly configured otherwise.

Those files may still participate in semantic indexing even when they are not routine prompt surfaces.

### Role of each file

`MEMORY.md` should answer:

- what is worth carrying forward in narrative form?
- what has changed the system's understanding of itself or its user?

`MEMORY.md` should not be treated as purely one thing.

It often contains both:

- identity-bearing continuity
- durable operational context

The spec should therefore treat `MEMORY.md` as a mixed file:

- some sections belong to the identity-preservation boundary
- other sections are just curated long-term memory and may be pruned, archived, or rebalanced

That identity-preservation boundary should be explicit in code as well as in prose. Ordinary `forget` / `reset` flows should preserve explicitly marked identity-bearing sections instead of treating the whole file as disposable runtime state.

`knowledge.md` should answer:

- what do we think we know?
- where did that knowledge come from?
- how confident are we?

`decisions.md` should answer:

- why was this choice made?
- what alternatives were considered?
- what did we expect to happen?

`rhizome.md` should answer:

- what concepts have become associated through use?
- what might be relevant laterally, not just hierarchically?

`questions.md` should answer:

- what still feels unresolved?
- what hypotheses or tensions should remain visible?

## Rhizome Model

The rhizome should not be treated as a fact store or a second `knowledge.md`.

Its job is different:

- preserve lateral association
- surface surprising adjacency
- reward recurrence
- forget through non-reinforcement

### Canonical systems model

At the systems level, the rhizome is best modeled as:

- a local association graph
- fed by reflection-time co-activation events
- projected outward as a human-readable `memory/rhizome.md`

In other words:

- machine truth = temporal association graph
- human view = `rhizome.md`

The Markdown file is a projection of the graph, not the deepest substrate.

### Graph elements

#### Nodes

Nodes represent concepts that recur in the system's life, such as:

- people
- projects
- motifs
- tensions
- recurring metaphors
- themes that bridge otherwise separate domains

#### Events

An event records that several concepts were co-activated during reflection, recap, or recovery.

Events should carry:

- timestamp
- scope
- source
- principal boundary
- salience

#### Edges

Edges are derived associations between nodes.

They should carry:

- strength
- recurrence count
- last reinforced time
- source mix
- decay state

### Write path

Rhizome updates should not be written impulsively from ordinary turns.

Preferred write path:

1. heartbeat/reflection reads recent notes, review events, and recap material
2. salient concepts are extracted
3. a co-activation event is recorded
4. edge strengths are updated
5. `memory/rhizome.md` is optionally regenerated or lightly amended as a projection

This keeps the rhizome emergent rather than performative.

### Read path

The whole rhizome should not be loaded into every turn by default.

Instead, it should act as an optional lateral suggestion engine:

1. identify active concepts in the current turn
2. activate nearby nodes
3. return a few lateral hints
4. treat those hints as associative prompts, not truth claims

This means rhizome output belongs closer to:

- heartbeat
- reflection
- optional governor ideation support
- optional Idolum coloration

It does not belong in the same truth category as:

- `knowledge.md`
- `decisions.md`
- direct user facts

### Governance

The rhizome must remain low-authority.

It must not:

- override `knowledge.md`
- outrank `decisions.md`
- silently become factual memory
- leak associations across principal boundaries

The correct hierarchy is:

- `knowledge.md` = claims
- `decisions.md` = provenance
- `MEMORY.md` = curated narrative
- `rhizome` = associative field

## Curated Memory vs Session Recall

This distinction is load-bearing.

### Curated memory

Curated memory is:

- compact
- durable
- intentionally maintained
- injected frequently

Examples:

- stable preferences
- environment facts
- persistent conventions
- lessons worth carrying forward

### Session recall

Session recall is:

- broader
- query-driven
- transcript-backed
- not injected by default

Examples:

- old conversations
- prior debugging sessions
- delivered review digests
- archived tool output summaries

The system should prefer:

- curated memory for "always useful, stable, compact facts"
- session recall for "find the earlier conversation about X"

## Source Tags and Confidence

Curated memory should not pretend every remembered thing is equally true.

Facts written into curated memory should support source tagging:

- `[direct]`
  - explicitly stated by the user or a trusted operator
- `[observed]`
  - inferred from repeated behavior or operational evidence
- `[inferred]`
  - reasoned from evidence, but not directly stated
- `[hypothesized]`
  - plausible but unconfirmed
- `[shared]`
  - arrived through shared fleet/project memory rather than direct operator contact

When appropriate, entries should also carry confidence.

Examples:

- `Prefers voice replies as mp3 [direct]`
- `Usually wants concise summaries before detail [observed, confidence: 0.8]`
- `May be avoiding this topic because it is emotionally loaded [hypothesized, confidence: 0.4]`

This is especially important once Aphelion supports:

- approved non-admin users
- review-event ingestion
- cross-session recall

The system should be able to say not only "I remember this" but "I remember why I believe this."

## Prompt Placement

Aphelion should keep the prompt layers distinct.

- constitutional files belong in the stable prefix
- curated dynamic memory files belong in the dynamic memory section
- recalled session context should be injected as an explicit fenced retrieval block, not blended invisibly into normal discourse

This is closer to Hermes' recalled-context fencing than to silently flattening all memory into one prompt body.
The attribution and departure record for this comparison lives in
[`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md).

Structured curated files should not all be blindly injected every turn.

The default loading strategy should be:

- stable constitutional files in the stable prompt prefix
- compact shared/principal memory files in the dynamic prompt section
- `knowledge.md` and `decisions.md` loaded only when compact enough or summarized into the dynamic section
- optional practice files such as `rhizome.md` and `questions.md` loaded on demand or in specialized maintenance turns
- transcript recall injected only when explicitly retrieved

This keeps the prompt coherent and prevents memory bloat.

When curated memory files exceed the practical working-set budget, Aphelion should prefer excerpting and archival over silent monotonic growth. The prompt-visible working set should stay bounded even if the underlying memory archive grows over time.

Semantic hits should never be flattened into the default prompt body just because an index exists. They should enter the turn only through explicit retrieval.

## Semantic Retrieval

Aphelion should support semantic retrieval as a deliberate, governor-owned capability.

The first principle is:

- semantic retrieval is query-driven
- semantic retrieval is not ambient injection

### Turn-time semantic retrieval

Live turns need:

- fast retrieval
- tight chunking
- bounded hit counts
- strong relevance ranking
- explicit governor invocation

This should look like a tool, analogous to `session_search`, not like always-on memory bleed.

Recommended initial sources:

- `MEMORY.md`
- `memory/knowledge.md`
- `memory/decisions.md`
- optionally recent daily notes

Optional advanced practice files such as `memory/questions.md` and `memory/rhizome.md` may be indexed too, but their hits should remain clearly lower-authority than claims from `knowledge.md` or `decisions.md`.

### Heartbeat / reflection retrieval

Heartbeat and reflection need a different semantic mode:

- broader chunking
- slower cadence
- lower sensitivity to exact phrasing
- better support for recurrence, contradiction detection, and thematic clustering

This mode should not be implemented as a normal turn-time tool call. It is a maintenance retrieval path.

It exists to help heartbeat ask questions like:

- what facts appear semantically redundant or contradictory?
- what notes keep recurring across days?
- what decisions connect to recent review events?
- what themes should be promoted or questioned?

### One substrate, two query modes

Aphelion should prefer:

- one local semantic index substrate
- two retrieval modes on top of it

Those modes are:

- interactive semantic retrieval
- maintenance semantic retrieval

That keeps indexing unified while preserving the real difference between fast live recall and slower reflective analysis.

### Retrieval result shape

Semantic results should be bounded and explicit.

Each hit should carry at least:

- source file or corpus
- scope
- score
- excerpt
- optional type label such as `memory`, `knowledge`, `decision`, `daily_note`, `question`, `rhizome`

Semantic hits should be injected as fenced retrieved context, not disguised as constitutional truth or as normal user messages.

## Review Events

`review_events` are not shared memory themselves. They are bounded cross-session inputs into the admin conversation.

They may later influence shared memory, but they do not automatically become it.

In other words:

- review events are memory inputs
- they are not memory commits

## Reflection and Distillation

Memory quality depends less on storage and more on whether the system revisits and curates what it has observed.

Aphelion should treat reflection as a governor-owned maintenance practice, usually driven by heartbeat.

The reflection loop should:

1. read recent daily notes and recent review-event inputs
2. extract durable facts into curated memory
3. update `knowledge.md`, `decisions.md`, `MEMORY.md`, `rhizome.md`, or `questions.md` as appropriate
4. flag contradictions instead of silently overwriting them
5. keep the working memory set compact

Reflection should be the main bridge between:

- raw daily notes
- bounded review-event digests
- durable curated memory

Without this loop, memory files tend to become write-only storage.

## Decay and Temperature

Not everything should remain equally active forever.

Aphelion should use temperature-style memory decay concepts, especially for daily notes and archives:

- `hot`
  - recent and/or recently read
  - eligible for immediate prompt inclusion
- `warm`
  - still relevant, but not always in prompt
- `cold`
  - candidate for summarization or archive indexing
- `frozen`
  - retained for history/retrieval, but not part of the working set

The goal is not deletion. The goal is keeping the active memory set small and alive.

At minimum, decay should apply to:

- daily notes
- archived review summaries
- large structured memory files

Shared daily notes should be treated narrowly:

- admin/maintenance daily notes may live in shared memory roots
- non-admin daily notes should remain per-principal by default

This preserves the isolation model and keeps "shared daily notes" from becoming ambient privacy bleed.

Curated high-value files such as `SOUL.md` and `IDENTITY.md` are not subject to ordinary decay.

## Identity Preservation Boundary

Not all memory is equal. Some files preserve continuity of self.

Identity-bearing files include:

- `SOUL.md`
- `IDENTITY.md`
- `IDOLUM.md`
- identity-bearing sections of `MEMORY.md`
- admin/operator profile content from `USER.md`

These should be treated as part of the identity-preservation boundary, not as disposable runtime memory.

If Aphelion resets, migrates, or rebuilds state, these files should survive differently from:

- session ledgers
- daily notes
- temp review artifacts
- caches and retrieval indexes

In other words:

- transcript history can be rebuilt or pruned
- identity continuity should be explicitly preserved

## Memory Tool

Aphelion should eventually expose a governor-owned `memory` tool for curated memory writes.

Initial actions should be:

- `add`
- `replace`
- `remove`

Targets should be explicit:

- `shared`
- `principal`

The tool should operate on curated memory surfaces, not on full session transcripts.

The tool should eventually be able to target structured files as well, for example:

- `shared:memory`
- `shared:knowledge`
- `shared:decisions`
- `principal:memory`
- `principal:knowledge`

## Session Search

Aphelion should eventually expose explicit session recall/search over the SQLite transcript ledger.

This is the right home for:

- cross-session recall
- "what did we discuss before?"
- finding prior tool-backed work

This should be architecturally separate from curated memory writes, even if both are called "memory" colloquially.

## OpenAI in the Memory Layer

OpenAI belongs in this spec, but not as the sole or canonical memory backend.

### Embeddings / semantic indexing

OpenAI may be used as an embedding provider for semantic recall over:

- memory files
- approved corpora
- later transcript collections

This is similar to OpenClaw's use of OpenAI in memory search: OpenAI is part of the retrieval infrastructure, while the underlying corpus remains local and operator-owned.
See [`docs/architecture/influences-and-departures.md`](../docs/architecture/influences-and-departures.md)
for the attribution and departure record behind this reference.

### File storage

OpenAI files should be treated as external durable objects usable by higher-level memory workflows.

Intended uses:

- upload source documents for retrieval
- stage files for later vector-store attachment
- keep external copies of memory-related documents when useful

When configured, this should be exposed as an explicit admin-governed tool surface rather than a hidden side channel.

### Vector stores

OpenAI vector stores are retrieval storage, not constitutional memory.

Use cases:

- attach uploaded files to retrieval indexes
- store parsed/chunked document representations
- power search over approved corpora

When configured, this should be exposed as an explicit admin-governed retrieval tool surface.

### Rule

No OpenAI object should silently replace:

- workspace files as the source of constitutional truth
- local curated memory as the source of durable operator-facing memory
- SQLite session ledgers as the source of conversational history

## Interfaces

### OpenAI files

```go
type FileStore interface {
    Put(ctx context.Context, localPath string, purpose string) (*StoredFile, error)
    Get(ctx context.Context, fileID string) (io.ReadCloser, *StoredFile, error)
    Delete(ctx context.Context, fileID string) error
    List(ctx context.Context, purpose string) ([]StoredFile, error)
}

type StoredFile struct {
    ID        string
    Filename  string
    Bytes     int64
    Purpose   string
    CreatedAt time.Time
}
```

### Retrieval stores

```go
type RetrievalStore interface {
    CreateStore(ctx context.Context, name string) (*VectorStore, error)
    AttachFile(ctx context.Context, storeID string, fileID string) error
    Search(ctx context.Context, storeID string, query string, limit int) ([]RetrievalHit, error)
}

type VectorStore struct {
    ID        string
    Name      string
    CreatedAt time.Time
}

type RetrievalHit struct {
    FileID   string
    Score    float64
    Content  string
    Metadata map[string]string
}
```

## Ownership Rules

- local workspace is the constitutional truth
- shared curated memory is admin-owned
- per-principal curated memory is principal-owned
- session ledgers are the truth of past conversation
- session recall/search reads ledgers; it does not replace them
- OpenAI files/vector stores/embeddings are auxiliary infrastructure
- OpenAI storage tools are admin-facing by default unless a narrower policy is designed later

## Config

```toml
[agent]
prompt_root = "~/.aphelion/agent"
shared_memory_root = "~/.aphelion/agent"
user_memory_root = "~/.aphelion/state/isolated/memory"
dynamic_files = ["MEMORY.md", "HEARTBEAT.md"]

[memory]
session_search = false
semantic_indexing = false

[memory.reflection]
enabled = true
every = "6h"

[memory.aggressive]
enabled = false
capture_every_turn = false
prefetch_every_turn = false
flush_on_session_boundary = false

[memory.decay]
enabled = true
hot_days = 3
warm_days = 14
cold_days = 30

[memory.identity]
preserve = ["SOUL.md", "IDENTITY.md", "IDOLUM.md", "MEMORY.md"]

[openai.files]
enabled = false
purpose = "assistants"

[openai.vector_stores]
enabled = false
default_store = ""

[openai.embeddings]
enabled = false
provider = "openai"
model = "text-embedding-3-small"
```

### Aggressive Memory Mode

`memory.aggressive` is an optional high-retention mode that trades additional model calls for stronger continuity.

- `capture_every_turn`: runs a bounded extraction pass after each committed interactive turn and appends durable items into curated stores.
- `prefetch_every_turn`: performs semantic recall before governor execution and injects a bounded `AUTO_RECALL_MEMORY` block into turn context.
- `flush_on_session_boundary`: flushes recent session context into curated memory when user boundaries are invoked (`/stop`, `/new`, `/restart`).

## Tests

### Current Phase

- **TestSharedMemoryReadOnlyForNonAdmin**
- **TestPerUserMemoryWritable**
- **TestWorkspacePromptFilesLoadInExpectedOrder**
- **TestCuratedMemoryDoesNotRequireSessionRecall**

### Next Phase

- **TestStructuredCuratedMemoryFilesRemainDistinct**
- **TestKnowledgeEntriesPreserveSourceTags**
- **TestReflectionExtractsFactsFromDailyNotes**
- **TestDecayKeepsWorkingMemorySetSmall**
- **TestIdentityPreservationBoundaryExcludesTempArtifacts**
- **TestSessionSearchReturnsPriorTranscriptSnippets**
- **TestRecalledContextIsPromptFenced**
- **TestMemoryToolWritesSharedMemoryForAdmin**
- **TestMemoryToolWritesPrincipalMemoryForApprovedUser**

### Deferred OpenAI storage / retrieval

- **TestOpenAIFilePut**
- **TestOpenAIFileGet**
- **TestOpenAIFileDelete**
- **TestOpenAIVectorStoreCreate**
- **TestOpenAIVectorStoreAttachFile**
- **TestOpenAIVectorStoreSearch**
- **TestEmbeddingBackedRecallDoesNotReplaceLocalTruth**
