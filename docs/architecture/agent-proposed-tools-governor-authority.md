# Agent-Proposed Tools, Governor-Held Authority

## Summary

Agents should be able to notice capability gaps, propose new tools, and even help draft the implementation of those tools.

They should **not** be able to silently grant themselves new authority.

The right split is:

- **agents can imagine and propose capabilities**
- **the governor decides whether those capabilities become real**
- **registration/exposure of tools is an explicit system event, not a private inference**

This keeps agents generic without collapsing the distinction between *proposed power* and *authorized power*.

## Why this matters

If a child can infer a missing tool, draft a wrapper, and then act as if that tool already exists, the system loses one of its most important truth boundaries:

- what is merely imagined
- what is actually registered in trusted system state
- what is explicitly authorized

That boundary is foundational for trust.

A generic-agent architecture should mean:

> any agent can participate in capability formation through a standard review/register path.

It should **not** mean:

> any agent can mutate its own capability envelope when it thinks doing so would help.

## Proposed model

### 1. Capability gap detection

An agent notices it cannot complete a task with its current tool envelope.

Example:

- email child sees a job lead
- realizes inbox-only context is insufficient
- proposes a search capability to find external postings

### 2. Structured proposal

Instead of pretending the tool exists, the agent emits a structured proposal such as:

- desired tool name
- reason for need now
- one contract blob (inputs/outputs/constraints)

Example proposal:

```json
{
  "proposal_id": "tp_123",
  "proposed_by": "idolum-email",
  "tool_name": "search_web",
  "why_now": "Inbox-only analysis cannot evaluate external postings.",
  "contract": {
    "inputs": {"query": "string", "limit": "int<=5"},
    "outputs": [{"title": "string", "url": "string", "snippet": "string"}],
    "constraints": ["read_only", "no_clickthrough", "max_3_queries_per_task"]
  },
  "review_status": "proposed"
}
```

### 3. Governor review

The governor evaluates:

- Is the capability real or redundant?
- What is the privacy scope?
- What is the external-effect surface?
- Is the interface narrow enough?
- Should it be reusable across agents?
- Does it require human ratification?

### 4. Human ratification when needed

If the tool widens capability, touches external systems, or changes privacy risk, it should go through explicit approval.

### 5. Registration as a real system change

If approved, the system performs an explicit registration step:

- wrapper code may exist in a branch (non-authoritative by itself)
- tool schema enters the trusted registry
- registered tool state becomes visible
- capability can then be exposed to allowed agents
- this becomes visible in canonical operational state

### 6. Post-registration use

Only after registration and exposure can agents invoke the tool as a real capability.

## Architectural principle

The system should preserve a hard line between:

1. **proposal** — “I think this tool should exist”
2. **registration** — “this tool exists in trusted runtime registry state”
3. **authorization/exposure** — “this agent is allowed to use it”
4. **execution** — “the tool was actually invoked”

Implementation artifacts in a branch are repository facts, not capability-lifecycle state.
For v1, the capability lifecycle is: `proposed -> approved -> registered -> exposed`.

## Recommended implementation shape

## A. Add a first-class tool proposal artifact

Introduce a proposal object that can be emitted by agents and surfaced to the governor/human review path.

Suggested fields:

- `proposal_id`
- `proposed_by`
- `tool_name`
- `why_now`
- `contract` (inputs/outputs/constraints blob)
- `review_status = proposed|approved|rejected`
- `registered_tool_id` (optional link once registered)

This can initially live alongside the existing proposal/review machinery rather than inventing a separate hidden channel.

Minimal shape (v1):

```json
{
  "proposal_id": "tp_123",
  "proposed_by": "idolum-email",
  "tool_name": "search_web",
  "why_now": "Inbox-only analysis cannot evaluate external postings.",
  "contract": {
    "inputs": {"query": "string", "limit": "int<=5"},
    "outputs": [{"title": "string", "url": "string", "snippet": "string"}],
    "constraints": ["read_only", "no_clickthrough", "max_3_queries_per_task"]
  },
  "review_status": "proposed"
}
```

```json
{
  "tool_name": "search_web",
  "implementation_ref": "tool/search_web.go",
  "registered": true
}
```

```json
{
  "tool_name": "search_web",
  "principal": "idolum-email",
  "active": true
}
```

## B. Treat registration as a governor-owned operation

Add a bounded governor path that can:

- register the tool schema
- attach policy/capability metadata
- record the registration event

This should not happen inside an ordinary child turn.

## C. Separate tool existence from tool exposure

A tool may exist globally but only be exposed to certain agents or workflows.

That gives us a clean pattern:

- tool is governor-owned
- specific agents get delegated access
- capability widening remains legible

### Role-change semantics

Exposure should not be treated as sticky authority.

- effective access is checked at invocation time against current principal state and current exposure policy
- stale exposure records by themselves do not confer access

## D. Log the transition in canonical/operational state

The following should be inspectable:

- proposal created
- proposal approved/rejected
- tool registered
- tool exposed to agent X
- tool invoked by agent X

This is important for auditability and future autonomy work.

## E. Keep the first tool narrow

For the web-search use case, the first version should be deliberately limited.

Suggested rung 1:

- `search_web(query, limit)`
- public search only
- normalized result list only
- no click-through
- no page fetch
- per-task query cap

This keeps the first registered capability small, legible, and low-risk.
It is also deliberately **read-only and external** so the first real tool through
this pipeline cannot mutate canonical/operational state.

## Brave Search as the first example

Brave Search is a good candidate for the first governor-owned discovery tool because:

- simple API
- bounded result surface
- no need for crawling
- easy to normalize
- read-only behavior (no direct mutation path)

Guardrail: keep early canonical examples read-only until register/expose/audit
discipline is proven in production-like use. Introducing mutating tools should
be treated as a separate widening step with explicit ratification.

A staged ladder would look like:

1. **inbox only**
2. **inbox + search discovery**
3. **inbox + search + page fetch/read**
4. **inbox + search + browser-assisted inspection**

The email/job child would become the first consumer, not the special owner of the capability.

## What agents should be allowed to do

Agents should be allowed to:

- detect missing capability
- propose the interface
- draft wrapper code
- draft tests
- argue for constraints
- suggest where the tool belongs

Agents should not be allowed to:

- assume the tool exists before registration
- silently mutate the runtime tool registry
- widen their own permissions
- blur drafted code with trusted capability

## Why this is the better version of “generic agents”

This preserves two things at once:

- **generality**: any agent can help drive capability formation
- **governance**: authority still lives above the agent

That gives us a more reusable and trustworthy system than one-off agent-specific hacks.

## Minimal rollout plan

### Phase 1

- Write down the proposal schema
- Surface tool proposals through the existing proposal/review path
- Implement one narrow governor-owned tool (`search_web`)
- Expose it only to the email/job workflow

### Phase 2

- Let agents draft wrapper/test skeletons as artifacts
- Add register/expose lifecycle state
- Reuse the same path for other proposed tools

### Phase 3

- Add richer capability classes (fetch, browser, mailbox mutation, etc.)
- Keep each widening step explicit and reviewable

## Bottom line

Agents should be able to help create tools.

They should not be able to self-authorize those tools.

The durable pattern is:

> proposal by agent, ratification by governor, registration by system, exposure by policy, use by authorized agent.

That is how the system stays both generic and trustworthy.
