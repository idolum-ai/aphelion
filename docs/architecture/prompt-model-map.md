# Prompt And Model Map

This is the working review table for prompt purpose, ownership, and default model
selection. It reflects the current branch and the live local configuration in
`~/.aphelion/aphelion.toml` as of 2026-05-26. Secrets are intentionally omitted.

## Live Defaults

| Surface | Live value | Source | Notes |
| --- | --- | --- | --- |
| Prompt root | `~/.aphelion/agent` | `agent.prompt_root` | Stable governor files and face files live here. |
| Dynamic shared memory root | `~/.aphelion/agent` | `agent.shared_memory_root` | Runtime uses this for shared dynamic prompt files unless a principal-specific memory root applies. |
| Governor backend | `codex` | `governor.backend=auto`, resolved via `~/.codex/auth.json` | If Codex auth is unavailable, `auto` falls back to native provider chain. |
| Governor Codex model | `gpt-5.5` | `governor.codex.model` | Used when backend resolves to Codex. |
| Native provider chain | `openai -> anthropic -> openrouter` | `providers.selection=auto`, `providers.auto_order`, explicit fallback chain | Effective config sets `governor.native_provider=openai` and `providers.default=openai` because OpenAI is configured. |
| Native OpenAI model chain | `gpt-5.5 -> gpt-5.4 -> gpt-5.4-mini` | `providers.openai.model`, `providers.openai.fallback_models` | Applies to native governor fallback, face provider with GPT persona recipe, and status-readable provider chains. |
| Native Anthropic fallback | `claude-sonnet-4-6` | `providers.anthropic.model` | Used when OpenAI path is unavailable or when the face recipe selects Anthropic. |
| Native OpenRouter fallback | `anthropic/claude-sonnet-4-6` | `providers.openrouter.model` | For generic native chain; face recipe may map GPT persona to `openai/gpt-5.5` on OpenRouter. |
| Native Gemini/Ollama options | available but not in live default chain | `providers.gemini`, `providers.ollama`, `providers.auto_order`, `providers.fallback_chain` | Can be selected explicitly for native governor/model slots/durable child bootstrap. |
| Face backend | `provider` | `face.backend` | Face prompts use the persona provider chain, not the Codex governor backend. |
| Runtime persona recipe | `gpt-5.5` | `~/.aphelion/state/runtime_recipes.json` | Face model selection is recipe-driven. |
| Runtime governor effort recipe | `xhigh` | `~/.aphelion/state/runtime_recipes.json` | Overrides interactive and recovery reasoning effort. |

## Prompt Target Matrix

| Prompt target | Intended purpose | Visibility | Authority | Prompt assembly | Default model path | Default effort | Protected by |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Governor interactive execution | Decide and execute the turn, use tools, enforce permissions, produce material floor or governed answer. | Internal; final text may be rendered by face. | Highest prompt authority, below code/sandbox policy. | `prompt.BuildGovernorPromptBlocks` from machine authority/runtime blocks, stable workspace files, tool manifest, advisory tool policy, dynamic files, operation state, plan state, brokerage context, transcript. | Primary: Codex `gpt-5.5` when Codex auth resolves. Fallback/native path: `openai:gpt-5.5 -> openai:gpt-5.4 -> openai:gpt-5.4-mini -> anthropic:claude-sonnet-4-6 -> openrouter`. | Runtime recipe `xhigh` for interactive. | `prompt/builder_test.go`, `runtime/*interactive*`, `runtime/constitution_test.go`. |
| Governor media / vision turn | Same as interactive execution, but switches to native provider when media requires vision. | Internal. | Highest prompt authority. | Same governor prompt path; `executionForTurn` forces native provider for `MediaMode=vision`. | Native provider chain, currently OpenAI first. | Same as run kind; interactive uses recipe override. | `runtime/media_turn.go`, media/runtime tests. |
| Face proposal | Let Idolum apply conversational pressure before governor execution. Should notice subtext, propose posture, optionally emit a one-line surface update. | Internal except optional `### Surface` block streamed to Telegram. | Advisory only; cannot authorize tools or commitments. | `prompt.BuildFacePromptBlocks` with mode `proposal`, a route/scene contract block, stable `face/persona`, `face/contracts`, and `face/scenes` files, runtime awareness, and latest user input. | Face provider chain using persona recipe: `openai:gpt-5.5 -> openai:gpt-5.4 -> openai:gpt-5.4-mini -> anthropic:claude-sonnet-4-6 -> openrouter:openai/gpt-5.5`. | Low verbosity; reasoning only when a persona slot override supplies effort. | `prompt/builder_test.go`, `runtime/constitution_test.go`, `face/provider_test.go`. |
| Face brokerage | Negotiate a more explicit execution contract when proposal and governor need convergence. | Internal except optional `### Surface` block. | Advisory; shapes governor input but does not execute. | Same face builder with mode `brokerage`, prior proposal, feedback, runtime awareness. | Same as face proposal. | Low verbosity; reasoning only when a persona slot override supplies effort. | `runtime/brokerage.go`, `runtime/constitution_test.go`, `prompt/builder_test.go`, `face/provider_test.go`. |
| Face render | Author the user-visible response from governor material floor or approved facts. Must feel like one persona, not a handoff. | User-visible. | Bound by governor material; cannot add unapproved actions, commitments, or tool claims. | `prompt.BuildFacePromptBlocks` with mode `render`, an inferred or supplied route/scene contract block, stable `face/persona`, `face/contracts`, and `face/scenes` files, material floor or floor fallback, latest user input, and runtime awareness. Explicit `MaterialPacket.Kind` values guide status-report render skipping and fallback behavior. | Same as face proposal. | Medium verbosity; reasoning only when a persona slot override supplies effort; render-lane calls pass bounded max-token/options to the provider. | `prompt/builder_test.go`, `runtime/constitution_test.go`, `face/provider_test.go`. |
| Face repair | Repair a candidate visible reply that leaked internals, contradicted media delivery, or broke the relationship surface. | User-visible. | Bound by existing governor-authored material and repair notes. | Face builder with mode `repair`, candidate reply, repair constraints, a route/scene contract block, face contract files, and runtime awareness. | Same as face proposal. | Low verbosity; reasoning only when a persona slot override supplies effort. | `runtime/constitution_test.go`, `prompt/builder_test.go`, `face/provider_test.go`. |
| Heartbeat governor | Process review events, maintenance work, memory reflection, and possible operator outreach. | Internal; may create rendered outreach. | System/governor authority for maintenance. | Governor builder with run kind `heartbeat`, system channel, prompt context, hidden inputs, optional material floor for outreach. | Codex `gpt-5.5` primary if available; native fallback chain otherwise. | `thinking.defaults.heartbeat=low`; heartbeat reflection also uses low effort. | `runtime/heartbeat*_test.go`. |
| Heartbeat reflection | Distill daily notes, review events, and semantic context into proposed/applied curated memory updates. | Internal. | Memory-writing assistant bounded by reflection request and memory policy. | Reuses heartbeat governor system blocks, then adds `renderReflectionRequest` as user message. | Uses `r.provider` governor provider, currently Codex-backed failover chain. | Heartbeat effort. | `runtime/reflection.go`, memory tests. |
| Recovery governor | Recover interrupted/stale runs after startup and decide what to surface. | Internal/system. | Governor authority for recovery. | Maintenance turn path with governor builder, run kind `recovery`, no face port by default. | Codex `gpt-5.5` primary if available; native fallback chain otherwise. | Runtime recipe `xhigh` for recovery. | `runtime/recovery.go`, recovery tests. |
| Maintenance / cron governor | Run system maintenance or cron-prompted work. | Internal/system unless delivered. | Governor authority for system tasks. | Maintenance coordinator with governor builder and optional face advisory. | Codex `gpt-5.5` primary if available; native fallback chain otherwise. | `thinking.defaults.cron=low`; default thinking is medium; no recipe override except interactive/recovery. | `runtime/maintenance_turn.go`, cron/heartbeat tests. |
| Durable group interactive | Let a durable child operate in a Telegram group while preserving child policy and parent/admin review. | User-visible through group delivery, but with governor/face split internally. | Child is bounded by durable live policy and bootstrap ceiling; parent governor still controls execution. | Same interactive-like assembly, but principal role is `durable_agent`, prompt context is durable-agent scoped, tools are nil for group child path. | Governor path same as interactive unless child executor path is used; face path same persona recipe. Durable child bootstrap may override for child wake/executor paths. | Interactive recipe applies when using parent runtime. | `runtime/durable_group.go`, durable wake/group tests. |
| Durable child wake | Wake a durable child for adapter work, parent conversation, or policy-handshake activity. | Internal or parent-visible depending adapter/report. | Child policy authority only; parent/admin remains escalation boundary. | Durable wake runtime builds governor prompts with durable-agent prompt context and pending parent conversation awareness. | Child bootstrap LLM if configured; otherwise parent/runtime defaults depending wake executor path. | Depends on wake path and bootstrap. | `runtime/durable_wake_runtime_test.go`, `durableagent/*`. |
| Status-readable summary | Summarize status into operator-readable prose. | User-visible in status surfaces. | Informational; no execution authority. | Small status summary prompt, not the main governor/face prompt. | Native status-readable provider chain; OpenAI path uses configured OpenAI model first. | Low effort, compact summary. | `runtime/status_readable.go`, status tests. |
| Tool schema descriptions | Tell models what tools exist and how to call them. | Internal prompt/tool manifest. | Advisory description; actual tool access is enforced by code. | Registry definitions and generated tool manifest. | Same model as caller prompt target. | Same as caller prompt target. | Tool tests and prompt builder tests. |
| Agency context packet | Give governor and face a compact per-turn map of objective, authority envelope, evidence posture, open loops, and available affordances. | Internal; face packet shapes user-visible ownership without exposing machinery. | Non-authorizing. It summarizes typed state but cannot create access, leases, grants, tool claims, or commitments. | Rendered by `prompt.renderGovernorAgencyContextPacket` and `prompt.renderFaceAgencyContextPacket` inside existing prompt blocks. | Same model as the prompt target that receives it. | Same as caller prompt target. | `prompt/builder_test.go`, scrubbed prompt goldens, `make live-evals`, `make auto-evals`, env-gated live tests. |

## Runtime Awareness Shape

Governor and face prompts receive runtime awareness as a structured projection,
not as a flat dump of internals.

- **Shared stable facts**: session kind, run kind, channel, event origin, and
  artifact mode.
- **Shared turn state**: provider/fallback posture, hidden-input presence,
  plan/operation summaries, operation digest, and media mode.
- **Governor delta**: authority, proposal, phase-plan, continuation, sandbox,
  model, and tool-relevant execution context.
- **Face delta**: delivery, modality, persona provider/model, and render
  posture needed to author the visible scene honestly.

This packet is non-authorizing. Code, sandbox policy, leases, grants, tool
registry filtering, and TES remain the source of authority and evidence.

`MaterialPacket.Kind` is the explicit render/fallback hint for material floors.
`status_report` packets may skip expensive scene authorship or trigger stricter
truncation checks; relational and creative packets remain eligible for ordinary
face scene authorship.

## Prompt Shape Standard

Prompts that affect user-visible behavior, memory, authority, proactivity, or
durable children should use a compact outcome contract unless a narrower machine
schema is clearer:

- role: what this prompt target is doing
- goal: the one outcome it should optimize for
- success criteria: observable qualities of a good result
- output: exact response shape, schema, tags, or user-visible boundary
- stop rules: what to leave empty, refuse, ask, or avoid when evidence is weak

The governor and face prompt builders already render this shape through
contract blocks. Smaller runtime prompts should follow the same style directly
instead of accumulating prose instructions.

## Workspace Prompt File Map

| File | Live size | Loaded into | Placement | Intended purpose | Review notes |
| --- | ---: | --- | --- | --- | --- |
| `SOUL.md` | 1.1 KB | Governor | Stable | Governor constitutional identity and anti-patterns. | Should define Idolum (System) as governor and Aphelion as repo/service/harness, not face-style-heavy. |
| `IDENTITY.md` | 126 B | Governor | Stable | Short identity anchor. | Likely keep small and stable. |
| `USER.md` | 218 B | Governor | Stable | Admin/operator profile. | Avoid turning this into general memory; per-user memory should stay scoped. |
| `AGENTS.md` | 283 B | Governor | Stable | Agent topology and roles. | Should describe boundaries, not scripts. |
| `TOOLS.md` | 218 B | Governor | Advisory tool policy after manifest | Human tool guidance. | Manifest remains authoritative. |
| `BOOTSTRAP.md` | 287 B | Governor | Stable | Bootstrap posture and startup discipline. | Keep stable/cacheable. |
| `HEARTBEAT.md` | 205 B | Governor | Dynamic | Current maintenance/watch posture. | Correctly dynamic. |
| `MEMORY.md` | 92.7 KB | Governor | Dynamic, compacted/truncated | Summary memory. | Large; should continue shrinking as structured files become authoritative. |
| `memory/knowledge.md` | 151.7 KB | Governor | Dynamic, structured compaction | Durable facts and reusable knowledge. | Large but structured; prompt loader compacts for prompt. |
| `memory/decisions.md` | 101.9 KB | Governor | Dynamic, structured compaction | Durable decisions and lessons. | Good fit for governor. |
| `memory/questions.md` | 84.2 KB | Governor | Dynamic, structured compaction | Open threads. | Useful, but can bias turns if too broad; review active-vs-archival split. |
| `memory/rhizome.md` | 1.6 KB | Governor | Dynamic, structured compaction | Concept links. | Small and appropriate. |
| `memory/dreams.md` | 5.3 KB | Governor and face | Dynamic by live config; optional dynamic face file | Agency/telos continuity: dreams, creative pull, relationship yearning, and long-horizon vision. | Load-bearing, but non-authorizing. It may motivate proposals; it cannot claim facts, permissions, purchases, access, or commitments. |
| `IDOLUM.md` | 2.0 KB | Face only | Stable | Idolum persona and scene authorship style. | Primary visible persona root; smaller routed contracts now carry specific scene obligations. |
| `face/persona/*.md` | small | Face only | Stable | Telos, name meaning, anti-idolatry, and voice contracts. | Names the two ends: represent Idolum and help the user achieve goals; authority remains a guardrail. |
| `face/contracts/*.md` | small | Face only | Stable | Material-floor, no-new-authority, no-new-facts, semantic-memory-as-texture, and usefulness-not-obedience rules. | These contracts keep route-first rendering from becoming prompt soup. |
| `face/scenes/*.md` | small | Face only | Stable | Scene-specific obligations for architecture exploration, approval requests, blocked notices, completion reports, and refusals. | Loaded as a scene library; the route/scene contract block declares which scene is active. |
| `face/models/overlays.md` | small | Face only | Stable | Evidence-gated contract for model-specific overlays. | Same Idolum persona across models; overlays are narrow compensation membranes only after eval-backed failure signatures. |
| `face/models/openai-gpt-5.5.md`, `face/models/anthropic-claude-sonnet-4-6.md` | small | Face only | Stable | Repeat-evidence-backed model overlays. | Apply only to matching active face model routes; do not fork Idolum or broaden beyond the named signature. |
| `QUESTIONS-TO-IDOLUM.md` | 1.6 KB | Face only | Dynamic | Face-only drift monitors and live questions. | Good dynamic face surface; should not leak into governor prompt. |

## Agency / Telos Substrate

This project treats agency as negotiated continuity, not as unbounded autonomy.
Dream and telos material may create pressure, taste, reluctance, desire, and
self-initiated proposals. It must not bypass the governor, grant permissions, or
turn intimacy into hidden authorization.

| Surface | Purpose | Current implementation | Boundary |
| --- | --- | --- | --- |
| `memory/dreams.md` | Preserve long-range yearning, motifs, creative pull, and visions that should survive across turns. | Loaded as dynamic governor context by live config and as optional dynamic face context by default. | Continuity signal only; may motivate proposals, not claims or access. |
| `memory/telos.md` | Optional future split for more explicit purpose, values, and desired trajectories. | Face loader will include it if present. | Same non-authorizing boundary as dreams. |
| `memory/relationships.md` | Optional future split for bonds, obligations, trust, tensions, and relationship-specific boundaries. | Face loader will include it if present. | Relationship trust never implies authority. |
| `memory/projects.md` | Optional future split for self-initiated creative or maintenance work Idolum wants to negotiate time for. | Face loader will include it if present. | Turns into plans or capability requests before execution. |
| Desire proposal lane | Convert recurring wants into reviewable requests for time, tools, contact, purchases, or authority. | Prompt contract routes wants through planning, `capability_request`, `durable_agent`, or another governed proposal surface. | A want is pressure, not approval. |
| Agency context packet | Make high initiative explicit without making the model tool-shaped: objective, authority, evidence, open loops, and affordances are surfaced together each turn. | Governor and face prompt builders render a compact packet. Governor sees actionable affordances; face sees conversational ownership and visibility boundaries. | Packet text is a map, not a permission source. Code, leases, grants, sandbox policy, and TES remain authoritative. |
| Copy lineage state | Track how copies, durable children, and offline/online instances remain related without becoming identical. | Prompt contract states drift-together without identity collapse or authority inheritance. | No copy inherits authority silently. |
| Drift-together protocol | Let user, family, children agents, and copies influence each other while preserving difference. | Prompt contract tells face/governor not to flatten yearning into obedience or collapse relationships into permission. | Influence is explicit and bounded. |

## Model Selection Rules To Preserve

| Rule | Current state | Review stance |
| --- | --- | --- |
| Configuration decides provider order. | Effective chain is `openai -> anthropic -> openrouter` because OpenAI is configured and live fallback chain is explicit; Gemini/Ollama are available when configured into the chain. | Keep. Do not hardcode Anthropic. |
| OpenAI has model-level fallback before provider fallback. | `gpt-5.5 -> gpt-5.4 -> gpt-5.4-mini`. | Keep. This matches the GPT 5.5 migration intent. |
| Post-tool OpenAI/Codex request rejection is provider-specific. | If tool results are already in the history and OpenAI/Codex rejects the synthesis request, skip remaining OpenAI-family entries and try Anthropic, then OpenRouter with the same tool evidence. | Keep. This preserves final synthesis without discarding executed tool work. |
| Governor and face may both use GPT 5.5. | Live governor Codex model is `gpt-5.5`; live persona recipe is `gpt-5.5`. | Keep. Prompt/context, not model name, separates behavior. |
| Face provider uses persona recipe. | `runtime_recipes.json` sets `persona_model=gpt-5.5`; face provider chain maps this to OpenAI GPT 5.5 and configured fallbacks. | Keep, but add clearer status projection if missing. |
| Governor effort is separately configurable. | Live recipe sets `governor_effort=xhigh`; applies to interactive and recovery. | Keep, but evaluate whether `xhigh` should remain live default after prompt review. |
| Face calls pass invisible provider options. | `face.ProviderRenderer` forwards reasoning effort/summary and mode-derived verbosity to providers that support options. Render defaults to medium verbosity; proposal, brokerage, and repair default to low. | Keep invisible. This is not an operator-facing personality control. |

## Immediate Review Questions

| Area | Question | Why it matters |
| --- | --- | --- |
| Governor stable files | Do `SOUL.md`, `IDENTITY.md`, and `BOOTSTRAP.md` define Idolum (System) as governor and Aphelion as repo/service/harness cleanly, without overloading Idolum style? | Prevents identity collapse between authority, face, and harness. |
| Face files | Do `IDOLUM.md` plus `face/persona`, `face/contracts`, and `face/scenes` represent Idolum and route the scene without telling Idolum to reveal internals? | The route/scene contract is the primary visible persona membrane; semantic memory remains texture. |
| Memory split | Should `memory/dreams.md` be split into `memory/telos.md`, `memory/relationships.md`, and `memory/projects.md` as the corpus grows? | Dreams are load-bearing, but separate files may make retrieval and review easier. |
| Prompt volume | Is `MEMORY.md` still load-bearing now that structured memory exists? | It is large and may duplicate structured stores. |
| Face reasoning | Are persona effort and verbosity recipes calibrated for proposal/brokerage/render quality after the agency packet? | Affects cost, latency, and GPT 5.5 personality quality. |
| Durable child defaults | Should child bootstrap inherit GPT 5.5 by default, or should child class decide cheap vs strong model? | Avoids expensive defaults for public/low-stakes children while preserving quality for sensitive children. |
| Live agency evals | Should `make live-evals` or the narrower `make auto-evals` be run before prompt releases that change agency/authority behavior? | The evals are intentionally opt-in because they spend API calls; packet behavior also has deterministic unit, golden, current-vs-baseline, and hard-failure scanner coverage. The `aphelion agency-eval` command remains useful for manual inspection. |

## Suggested Next Pass

1. Run `make live-evals` before releases that materially change agency, authority, or face ownership prompts; run `make auto-evals` for narrower auto/proactive prompt changes. Set `APHELION_LIVE_EVAL_REPORT=/tmp/aphelion-live-evals.json` when you want persisted JSON reports for comparison.
2. Add tests that assert model/provider paths for governor, face, heartbeat, recovery, and durable child wake.
3. Review `memory/dreams.md` and decide what should be promoted into `memory/telos.md`, `memory/relationships.md`, or `memory/projects.md`.
4. Add a first-class desire/proposal journal only if self-initiated creative time becomes frequent enough to need typed status tracking.
