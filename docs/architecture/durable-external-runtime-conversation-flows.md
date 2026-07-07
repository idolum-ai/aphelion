# Durable External Runtime Conversation Flows

_Status: UX baseline companion to the durable external runtime proposal; not
implemented runtime behavior._

These flows are UX baselines for mockups, not literal transcript fixtures. They
should be reconstructed from typed tests, logs, and operational incidents where
possible, then polished only enough to show the intended conversation design.
Each mock should distinguish what the user sees from the authority truth
underneath: active SOW version, matched condition, materialized leases,
reviewer route, typed blocker, or accepted result.

## Setup And Trial

- Create a child SOW: Admin defines the child's charter, customer, runtime,
  schedule, credentials, review routes, and policy ceiling.
- Approve initial SOW: Aphelion summarizes the proposed SOW and asks the
  authority principal to sign, revise, or reject it.
- Trial run with admin review: The child completes routine work in supervised
  mode while the Aphelion admin validates safety and usefulness.
- Trial graduation: After successful trial runs, Aphelion proposes moving
  routine content/domain approval from admin to the customer review principal.
- Customer reviewer onboarding: The customer is added as review principal for
  drafts, audience updates, and routine domain decisions without receiving
  platform authority.
- Split approval routing: Aphelion routes platform changes to the authority
  principal, content review to the customer, and private-resource consent to the
  resource owner.

## Routine Work

- Scheduled email review: A daily trigger materializes Gmail read and WhatsApp
  draft leases for this wake only.
- Direct customer chat with child: An admitted customer talks directly with the
  OpenClaw/Hermes-backed child persona, and the child replies in the same
  conversation without parent review for every turn.
- Direct chat asks for an effect: The customer asks the child to read Gmail,
  send to a list, open a browser, call a webhook, or change its SOW; the adapter
  routes the effect request through Aphelion for a lease, review, blocker, or
  amendment proposal.
- Customer email draft approval: The child drafts an update and the customer
  approves content, recipients, and timing.
- Draft before send: The child may draft through Hermes/OpenClaw, but delivery
  waits for the configured review route.
- Autonomous named-audience send: A signed SOW version permits sending only to
  named audiences under explicit outbound policy.
- Friday browser watch: A scheduled browser task runs for a bounded duration
  against named domains.
- IFTTT alert dispatch: The child calls only the allowlisted endpoint, method,
  and payload schema named by the materialized lease.
- Public channel draft review: A child prepares a public-channel reply, but
  outbound delivery waits for customer or admin review according to the SOW.

## Operating Rhythm

- Daily parent-child standup: Parent Aphelion asks the child what it did, what
  is blocked, what the next scheduled wake is, and whether the SOW still fits.
- Daily customer digest: Parent Aphelion sends the customer a concise summary
  of completed work, pending reviews, blocked items, and upcoming scheduled
  work.
- Child blocker standup: The child reports missing credentials, stale runtime
  status, or review delays as typed blockers instead of retrying silently.
- SOW health review: Parent Aphelion compares actual work, exceptions, failures,
  and skipped schedules against the current SOW.
- End-of-week SOW retrospective: Parent, child, and customer review outcomes,
  recurring exceptions, proposed amendments, and autonomy level.

## Exceptions And Renegotiation

- Exception approval: The child asks for one unplanned action outside the SOW;
  Aphelion requests a narrow one-time approval from the correct principal.
- Repeated exception pattern: Aphelion notices repeated approvals of the same
  shape and suggests a formal SOW amendment.
- Child-proposed amendment: The child proposes an amendment as review evidence
  with a structured risk delta, not as authority.
- Customer-proposed amendment: The customer asks for broader work; Aphelion
  drafts an amendment and routes any authority widening to the admin.
- Approve SOW amendment: Admin signs a new SOW version; future leases bind to
  that version.
- Reject SOW amendment: Admin rejects the proposal; the active SOW and current
  lease materialization rules remain unchanged.
- Emergency narrowing: Admin revokes or narrows a grant and future leases stop
  materializing immediately.

## Boundaries And Failures

- Missing credential blocker: A scheduled wake matches, but Aphelion blocks
  before adapter invocation because a child credential is missing, expired, or
  stale.
- Runtime preflight failure: Hermes/OpenClaw install, source, env, dependency,
  or drift checks fail before child execution.
- Child wake lease missing: A child has grant coverage but no current
  `child_wake` lease, so Aphelion asks for the exact wake lease instead of
  looping.
- Old lease replay denied: A runtime tries to reuse a lease from an older SOW
  version and Aphelion blocks it.
- Unknown sender handling: A gateway receives an unknown sender and routes
  pairing/review instead of admitting memory or authority.
- Ambient native tool denied: Direct chat continues, but an upstream
  Hermes/OpenClaw native tool call is blocked unless the adapter can route it
  through an Aphelion-brokered lease.
- Parent memory admission denied: A child-local conversation summary remains
  child memory until Aphelion accepts a governed parent-memory admission event.
- Terms boundary warning: Enabling a channel, provider, or hosted service
  surfaces external terms and credential requirements before activation.
- Reviewer unavailable: Customer review is pending too long; Aphelion follows
  the SOW fallback policy to wait, skip, or escalate.
- Multi-customer tenant boundary: A leased child cannot leak memory,
  credentials, channel state, or review authority across customers.
- Leased agent offboarding: Customer ends the lease; Aphelion parks the child,
  revokes credentials, stops schedules/gateways, and preserves audit evidence.

## Before And After Regression Flows

- Approval loop before/after: The old flow loops on unclear approval; the new
  flow names the missing grant or lease and presents one bounded action.
- Context shedding before/after: The old flow repeats stale context; the new
  flow summarizes typed blockers, retrieves relevant evidence, and asks for the
  next bounded approval or amendment.
- Admin bottleneck before/after: The old flow sends every content decision to
  Aphelion admin; the new flow routes customer-domain review to the customer
  while reserving platform authority for admin.
