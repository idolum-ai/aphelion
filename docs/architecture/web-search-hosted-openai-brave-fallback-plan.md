# Web Search Implementation Plan: Hosted OpenAI First, Brave Fallback

Status: implementation plan only
Branch: `docs/web-search-hosted-brave-plan-20260523`
Baseline: `origin/main` at `a074ce7`
Scope: native Aphelion web search design; no code implemented in this branch yet.

## Framing

Web search is **public-web evidence acquisition under explicit approval**, not ambient browsing.

That frame matters more than the provider choice. OpenAI hosted search and Brave Search are both external evidence sources: they spend quota, cross the sandbox boundary, and return untrusted public content that can steer a turn. Aphelion should therefore expose search only as a governed tool invocation with query, provider, attempt budget, grant, lease, and result provenance attached.

## 0. Why this shape

Daniel's design target is an in-between path:

- Aphelion already has OpenAI credentials for hosted web search.
- Brave Search can be configured later and should work as a fallback provider.
- Web search must always stay under approval because it creates external network effects, can spend quota/credits, and brings untrusted public content into the turn.

The comparison against local `/tmp` clones points to three useful precedents:

- OpenClaw has first-class Brave Search as a `web_search` provider plugin, with `BRAVE_API_KEY`, plugin config, web/LLM-context modes, caching, trusted endpoint handling, and explicit external-content wrapping.
- Hermes, in the inspected files, does not appear to have native Brave Search as a search backend. Its documented web backends are Firecrawl, Parallel, Tavily, and Exa; Brave appears mostly as Chromium/Browser CDP support and as OpenClaw migration compatibility.
- Codex, in the inspected clone, has no Brave-specific provider. It models web search as a hosted/provider capability with modes such as `disabled`, `cached`, and `live`, plus options like context size, domain filters, and location.

Aphelion should use the Codex-like hosted-tool model for OpenAI, but preserve OpenClaw's practical provider/fallback discipline and Aphelion's own authority ledger.

## 1. Design question

**Question:** What is the smallest native `web_search` surface that gives Aphelion useful search without bypassing approval gates?

**Answer:** Add an authority-managed native `web_search` tool that is exposed only when configured and granted, and that refuses to execute unless the current turn has active lease evidence. The tool should try hosted OpenAI web search first, then Brave Search only when fallback is allowed by the approved invocation contract.

### Strategy

Use a wrapper tool rather than giving the model ambient hosted web search directly.

The wrapper should:

1. enforce capability grant + active continuation/operation lease,
2. normalize a bounded query request,
3. dispatch to configured providers in order,
4. record provider attempts and capability invocation evidence,
5. mark all returned content as untrusted external content,
6. return structured JSON for the model to cite or summarize.

### Why not pass OpenAI hosted search directly into every model turn?

Direct hosted search exposure is too ambient for Aphelion's authority model. It would let a model decide to search inside a normal completion, making it harder to attach the exact query, provider, request count, grant, and lease evidence to a governed tool invocation.

For v1, hosted search should be a backend inside an Aphelion tool call, not a general model affordance.

## 2. Current repo anchors

The current repository already has most of the authority machinery needed:

- `tool/definitions.go`
  - filters visible tools by principal.
  - already has a native authority-managed special case for `codex_image_generation`.
  - exposes `capability_request`, `capability_authority`, and `tool_authority`.
- `tool/codex_image_generation.go`
  - good precedent for a native provider-backed tool requiring an active `kind=tool` capability grant.
  - records invocation status and returns explicit blockers.
- `tool/authority_access.go`
  - `authorityUseRefForGrant` requires active turn lease evidence.
  - capability invocation records include session, continuation lease, operation-plan lease, and authority source.
- `session/types_capability.go`
  - `CapabilityGrant`, `CapabilityInvocation`, and `AuthorityUseRef` already carry the needed audit fields.
- `provider/openai_responses.go`
  - currently maps Aphelion tools to OpenAI Responses function tools.
  - will need a builtin hosted-web-search mapping, analogous in spirit to the Codex image generation builtin mapping.
- `governorbackend/codex_request.go`
  - has `codexBuiltInToolSpec` for builtin `image_generation`.
  - useful pattern for translating an Aphelion `ToolDef` into provider-native tool JSON.
- `tool/native_fetch_network.go`
  - useful reference for allowlist and DNS-pinning posture, though Brave should not be implemented by calling the user-facing `fetch_url` tool.
- `docs/architecture/capability-delegation-lane.md`
  - canonical request/review/grant/observe/renew/revoke model.
- `docs/architecture/external-tools-pilot.md`
  - useful for lifecycle principles, but `web_search` should be native in v1, not an external manifest tool.

## 3. Proposed v1 architecture

### Components

1. `tool/web_search.go`
   - Defines `web_search` input/output types.
   - Enforces authority and lease evidence.
   - Dispatches to the provider chain.
   - Records provider attempts and capability invocation outcomes.

2. `tool/web_search_provider.go` or package `websearch`
   - Defines a small provider interface:

   ```go
   type Provider interface {
       Name() string
       Available(ctx context.Context, req Request) Availability
       Search(ctx context.Context, req Request) (Result, error)
   }
   ```

3. Hosted OpenAI provider
   - Uses a dedicated OpenAI Responses call with provider-native hosted web search enabled.
   - Must verify the exact OpenAI Responses web-search tool schema during implementation.
   - Returns structured results or a structured synthesized answer with citations.

4. Brave provider
   - Calls Brave Search API directly using a configured secret source.
   - Endpoint: `https://api.search.brave.com/res/v1/web/search` for v1.
   - LLM Context mode can be a later extension.

5. Config
   - Adds a `[tools.web_search]` section rather than overloading provider fallback config.
   - Keeps credentials in env/file/config-secret paths, never memory.

6. Tests and doctor/status projection
   - Unit tests with fake providers and `httptest`.
   - `/doctor` should report configured/not configured and grant/approval blockers without showing secrets.

## 4. Authority contract

**Question:** What does “always under approval” mean concretely?

It should mean all of these are true:

1. The tool is not generally usable just because credentials exist.
2. The caller needs an active `capability_grant`:
   - `kind = tool`
   - `target_resource = web_search`
   - `allowed_actions` contains `invoke`
3. The call must have active turn authority evidence:
   - active `ContinuationLease`, or
   - active `OperationPlanLease`
4. The lease/proposal contract must explicitly allow live/public web search or equivalent bounded action labels.
5. The grant/lease constraints must bound request count, providers, domains if supplied, and retention behavior.

The existing `codex_image_generation` pattern already enforces #2 and #3 through `capabilityGrantAllowsAuthorityToolAccess` and `authorityUseRefForGrant`. `web_search` should use the same pattern, then add provider/request-specific validation.

### Proposed grant constraints

A usable grant can carry constraints like:

```json
{
  "tool_invocation": {
    "actions": {
      "invoke": {
        "allowed_fields": [
          "query",
          "count",
          "freshness",
          "allowed_domains",
          "blocked_domains",
          "provider_policy",
          "context_size"
        ]
      }
    }
  },
  "web_search": {
    "providers": ["openai_hosted", "brave"],
    "max_queries_per_invocation": 1,
    "max_provider_attempts_per_invocation": 2,
    "default_count": 5,
    "max_count": 10,
    "cache_ttl": "15m",
    "retention": "do_not_write_to_memory_by_default",
    "external_content": "untrusted"
  }
}
```

The implementation should not rely only on prose in approval cards. It should parse typed constraints where present and fail closed when constraints are malformed.

## 5. Tool contract

**Question:** What should the Aphelion-facing tool look like?

Recommended name: `web_search`.

Recommended input:

```json
{
  "query": "string, required",
  "count": 5,
  "freshness": "day|week|month|year, optional",
  "allowed_domains": ["example.com"],
  "blocked_domains": ["example.org"],
  "provider_policy": "auto|openai_hosted|brave",
  "context_size": "low|medium|high"
}
```

Recommended output:

```json
{
  "status": "completed|blocked|failed",
  "query": "...",
  "provider": "openai_hosted|brave",
  "fallback_attempted": true,
  "attempts": [
    {
      "provider": "openai_hosted",
      "status": "unavailable|failed|completed",
      "error_class": "non_secret_string"
    }
  ],
  "external_content": {
    "untrusted": true,
    "source": "web_search",
    "provider": "..."
  },
  "results": [
    {
      "title": "...",
      "url": "https://...",
      "snippet": "...",
      "published": "optional",
      "site_name": "optional"
    }
  ],
  "blocker": "non_secret_string when blocked"
}
```

The result must never include provider credentials, request headers, raw logs, or hidden config paths.

## 6. Provider order and fallback semantics

**Question:** When should Brave fallback run?

Default provider order:

1. `openai_hosted`
2. `brave`

Fallback should run only when the active approval/grant allows the fallback provider and request budget.

### Safe fallback cases

Brave fallback is safe when OpenAI hosted search fails before a meaningful external search is executed, for example:

- OpenAI hosted search is not configured.
- The current OpenAI provider/model does not support hosted web search.
- The implementation cannot build a valid hosted web-search request.
- OpenAI rejects the request before tool execution with an unsupported-tool/config error.

### Budgeted fallback cases

Fallback may also be acceptable after an external attempt if the approved contract explicitly allows multiple provider attempts. Examples:

- OpenAI returns a retryable provider error.
- OpenAI quota/rate-limit prevents completion.
- Hosted search times out.

In those cases the output should say `fallback_attempted=true`, record the OpenAI failure class, then attempt Brave if within `max_provider_attempts_per_invocation`.

### Fail-closed cases

Do not fallback when:

- the grant/lease allows only one provider,
- the provider failure might indicate policy refusal rather than availability,
- the fallback would exceed spend/request budget,
- Brave is not configured,
- the request includes unsupported filters that cannot be safely mapped.

## 7. Hosted OpenAI provider plan

**Question:** How should hosted OpenAI search be wired without making it ambient?

Use a dedicated provider-backed adapter call from `web_search`, not the main turn's general tool list.

Implementation steps:

1. Add a way to inject a `webSearchHostedProvider agent.Provider` into `tool.Registry`, similar to `WithCodexImageGenerationProvider`.
2. Add OpenAI Responses builtin mapping for hosted web search in `provider/openai_responses.go`.
   - Verify exact current OpenAI Responses tool JSON before implementation.
   - Likely shape is a provider-native web search tool rather than a normal function tool.
3. Build a short internal prompt that asks the provider to perform exactly one search for the supplied query and return strict JSON.
4. Parse and validate the returned JSON.
5. If structured JSON fails, return a blocker or a conservative synthesized result with citations only if URLs are present.
6. Record status without storing raw provider bodies by default.

Open question for implementation: whether the active OpenAI model supports hosted search directly under the configured Responses endpoint. The plan should include a separate live smoke phase before declaring it working.

## 8. Brave provider plan

**Question:** How should Brave fallback be implemented?

Start with plain Brave Search API web mode.

Implementation steps:

1. Add config fields under `[tools.web_search.brave]`:

   ```toml
   [tools.web_search]
   enabled = false
   provider_order = ["openai_hosted", "brave"]
   max_count = 10
   timeout = "15s"
   cache_ttl = "15m"

   [tools.web_search.openai_hosted]
   enabled = true
   context_size = "medium"

   [tools.web_search.brave]
   enabled = false
   api_key_env = "BRAVE_API_KEY"
   endpoint = "https://api.search.brave.com/res/v1/web/search"
   ```

2. Resolve credentials only at call time.
3. Prefer env/file/config-secret references over raw config values.
4. Send `X-Subscription-Token` only in request headers.
5. Never log the header or token.
6. Map Brave results into Aphelion's normalized result shape.
7. Treat returned snippets as untrusted external content.
8. Add optional support for `country`, `search_lang`, `ui_lang`, and freshness after the v1 skeleton.

Brave LLM Context mode should not be in v1 unless Daniel explicitly wants it; it has different filter support and result semantics.

## 9. Caching and retention

**Question:** Should search results be cached?

Yes, but narrowly.

Recommended v1:

- cache successful provider results for a short TTL, default `15m`, keyed by normalized query + provider + filters;
- do not cache failed auth or policy blockers;
- do not write results to memory automatically;
- do not include raw HTML;
- record only non-secret provider attempt metadata in execution/capability invocation state.

This follows OpenClaw's practical cache discipline while preserving Aphelion's memory hygiene.

## 10. Network and spend boundaries

**Question:** How do we keep network/spend effects bounded?

- Hosted OpenAI search consumes OpenAI/provider quota; it must be represented as an approved web-search provider attempt.
- Brave consumes Brave Search API quota/credits; it must be disabled until a credential exists and the operator grants provider use.
- Both providers should respect per-invocation request budgets.
- `/doctor` should expose configuration status as:
  - `openai_hosted configured/unconfigured/support_unknown`
  - `brave configured/unconfigured`
  - `web_search grant active/missing`
  - `last_web_search_attempt status`, if available
- No code should automatically set up or validate credentials outside a separate approval.

## 11. Implementation stages

### Stage 1 — Authority skeleton

**Question:** Can Aphelion expose a native `web_search` tool that always blocks safely unless authority exists?

Strategy:

- Add `tool/web_search.go` with input parsing and blocker output only.
- Add `web_search` as a native authority-managed tool following `codex_image_generation`.
- Require active `kind=tool` grant and active lease evidence.
- Add tests proving no grant/no lease/provider missing all block.

Deliverable:

- `web_search` appears only for granted principals, or blocks at invocation with exact missing authority.

### Stage 2 — Hosted OpenAI adapter

**Question:** Can OpenAI hosted search be wrapped as a deterministic Aphelion tool call?

Strategy:

- Add registry injection for a hosted search provider.
- Add OpenAI Responses builtin mapping after verifying schema.
- Use fake-provider tests first; no live OpenAI tests in CI.
- Add strict result parsing and blocker behavior.

Deliverable:

- One fake hosted search test returns normalized results.
- One fake unsupported-provider test falls through to fallback policy.

### Stage 3 — Brave adapter

**Question:** Can Brave be a fallback without becoming ambient browsing?

Strategy:

- Add Brave config and credential resolution.
- Use `httptest` for API responses.
- Normalize result shape.
- Redact credential-bearing errors.
- Add no-token and unauthorized tests.

Deliverable:

- Brave provider works against local test server.
- No credential value appears in errors/log-like output.

### Stage 4 — Provider chain and fallback policy

**Question:** Can provider fallback follow Aphelion's side-effect discipline?

Strategy:

- Add provider attempt budget.
- Distinguish unavailable, pre-execution failure, post-external-attempt failure, rate limit, auth failure, and policy refusal.
- Allow fallback only when configured and approved.

Deliverable:

- Tests for hosted success, hosted unavailable -> Brave success, hosted failure without fallback permission -> blocker, Brave missing -> blocker.

### Stage 5 — Observability and operator UX

**Question:** Can Daniel see what happened without seeing secrets?

Strategy:

- Record capability invocations with provider attempt summary.
- Add progress renderer label for `web_search`.
- Add `/doctor` projection for web-search config/grant/provider status.
- Add docs explaining approval examples.

Deliverable:

- Operator can tell why search is unavailable or which provider was used.

### Stage 6 — Live smoke under separate approval

**Question:** Does it work with real provider credentials?

Strategy:

- Separate approval only.
- Test one harmless query with OpenAI hosted search.
- Test one harmless query with Brave only if Daniel has configured Brave credentials and explicitly approves the provider status/search check.
- Report only status and normalized results, never credentials.

Deliverable:

- Evidence-backed working/not-working status.

## 12. Test plan

Unit tests:

- `web_search` definition hidden or blocked without provider/grant.
- active grant but no active lease blocks.
- malformed input blocks.
- constraints limit `count` and allowed fields.
- hosted provider success normalizes results.
- hosted unavailable falls back only when policy allows.
- Brave provider no key blocks.
- Brave provider unauthorized returns non-secret error.
- Brave provider success maps titles, URLs, snippets, published age/site name.
- fallback attempt count obeys max attempts.
- output marks `external_content.untrusted=true`.

Integration-style tests without live network:

- `httptest` Brave endpoint.
- fake `agent.Provider` for hosted OpenAI adapter.
- session store capability grant + continuation lease evidence.

No live OpenAI or Brave calls in default tests.

## 13. Open questions before implementation

1. What exact OpenAI Responses hosted web-search schema should Aphelion target today?
2. Should `web_search` be visible to the model when a principal has a grant but no active lease, or hidden until session-key/lease-aware definitions exist? Current `DefinitionsForPrincipal` lacks a session key, so v1 probably exposes with grant and blocks at execution without a lease.
3. Should Brave LLM Context mode be a v2 feature? Recommendation: yes.
4. Should search cache live in session DB, process memory, or a small filesystem cache? Recommendation: process/session-local first, no durable memory writes.
5. Should fallback from OpenAI to Brave happen after a real external OpenAI attempt by default? Recommendation: only if the approval/constraints permit multiple provider attempts.

## 14. Non-goals

- No general browser automation.
- No scraping or arbitrary page fetch beyond search result snippets in v1.
- No automatic credential discovery from memory.
- No raw credential storage in memory or repo files.
- No ambient always-on web search.
- No commits, deploys, or service restarts as part of this plan phase.

## 15. Recommended first implementation PR

The first code PR should be narrow and blocker-heavy. Its purpose is not to prove web search works against live providers; its purpose is to prove that search cannot become ambient authority.

Hard acceptance criteria:

- `web_search` is hidden or returns a structured blocker without an active `kind=tool` grant.
- An active grant still blocks without active continuation or operation-plan lease evidence.
- The OpenAI hosted-search path is represented by a fake-provider test before any live-provider smoke test.
- Brave is not called until fallback policy, request budget, and credential resolution behavior are covered by tests.
- Default CI performs no live OpenAI, Brave, or public-web calls.
- `/doctor` or equivalent status projection can explain the current blocker: disabled config, missing provider support, missing grant, missing lease, missing credential, or fallback not allowed.
- All successful or blocked outputs mark public-web content as untrusted and exclude credential values, request headers, raw logs, and hidden config paths.

Suggested first slice:

1. Add config structs for `[tools.web_search]`.
2. Add a native authority-managed `web_search` skeleton with structured blockers.
3. Add tests for grant + lease enforcement and malformed constraints.
4. Add a fake hosted-provider adapter and one successful normalization test.
5. Add status/doctor projection for why search is unavailable.
6. Defer Brave live HTTP and any real-provider smoke test to a separately approved follow-up.

Then a second PR can add Brave provider + fallback chain.

This preserves Daniel's core idea while keeping Aphelion's floor intact: hosted OpenAI search first, Brave fallback second, and every live search tied to explicit approval evidence.
