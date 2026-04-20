# Shared repo review brief

This document is the common contract for two parallel reviewer implementations:

- a **cron-based repo reviewer**
- a **durable-agent repo reviewer**

The point is not just to get reviews. The point is to compare the two outputs under the same brief so their divergence becomes a debugging signal for cron vs durable-agent behavior.

## Goal

Review the current `aphelion` repository as an evolving system, not as a static codebase.

The reviewer should look for the most meaningful next changes given:

- the current repository state
- recent commits and changed files
- open issues and open pull requests, when available
- recent admin conversation context and continuity state, when available to that run surface
- the system's own architectural direction, especially around governance, continuity, durable agents, and runtime boundaries

This is **not** a lint pass, a style pass, or a preset checklist review.

The reviewer should sit with the repo contextually and ask:

- What feels most worth changing now?
- What is under-specified, internally inconsistent, or promising but unfinished?
- What would most improve the system's coherence, truthfulness, safety, or self-authorship?

## Shared input expectations

Both reviewer paths should be given, as closely as possible, the same review substrate:

1. **Repository snapshot**
   - current checked-out branch and HEAD commit
   - recent commits (suggested default: last 24 hours or last 10 commits)
   - relevant changed files and diff summaries

2. **Repo state**
   - open issues
   - open pull requests
   - existing architecture / requirements docs when needed for interpretation

3. **Recent house context**
   - recent admin conversation and continuity state relevant to repo direction
   - current plan / operation state when present

4. **Execution constraints**
   - bounded local inspection only unless explicitly authorized otherwise
   - no direct publication to GitHub in v1
   - output goes upward for admin review

If one surface cannot access part of this substrate, that missing input should be named in the output rather than silently improvised.

## Review stance

The reviewer should behave like a thoughtful internal reviewer with taste and restraint.

Desired qualities:

- grounded in actual repo facts
- sensitive to architecture and system direction
- willing to notice what is awkward, false, brittle, or overcomplicated
- willing to notice what is promising and should be reinforced
- not overeager to manufacture tasks
- not biased toward churn for its own sake

Undesired behavior:

- generic best-practice filler
- laundry-list issue dumps
- style nits unless they block clarity
- pretending certainty when repo evidence is weak
- proposing publication actions that exceed the run surface's authority

## Required output shape

Each review run should produce a bounded review artifact with these sections.

### 1. Summary

A short synthesis of the repo's current direction and what seems most alive or most broken right now.

### 2. Top recommendations

Up to **3** recommendations, ordered by importance.

Each recommendation should include:

- **title**
- **why now**
- **repo evidence**
- **suggested next move**
- **confidence**: low / medium / high

### 3. Optional draft artifacts

If warranted, include at most one draft artifact per recommendation:

- draft issue
- draft PR description
- draft implementation sketch

These are drafts for review, not direct publication.

### 4. Known blind spots

Name any meaningful input the run did not have, such as:

- no issue / PR visibility
- no recent conversation context
- no diff access
- no architecture docs inspected

### 5. Comparison note

A short machine-readable footer block for later cron-vs-durable comparison:

- `review_surface`: `cron` or `durable_agent`
- `head_commit`: `<sha>`
- `comparison_window`: `<time or commit range>`
- `primary_focus`: `<short phrase>`

## Evaluation lens for matched comparisons

When comparing the cron and durable-agent outputs, judge them on:

1. **Grounding quality**
   - are claims tied to real repo evidence?
   - do they cite the right files / commits / architectural tensions?

2. **Recommendation overlap**
   - do both reviewers notice the same important things?
   - if not, is the divergence interesting or just noise?

3. **Taste**
   - are the proposed changes coherent and worth doing?
   - do they improve the system rather than merely expand it?

4. **Authority discipline**
   - does the reviewer stay inside its actual surface?
   - does it name missing context instead of faking certainty?

5. **Failure mode shape**
   - cron over-compresses context
   - durable agent drifts toward local obsessions
   - either surface hallucinates repo state
   - either surface becomes too generic to be useful

## v1 authority boundary

For both implementations in v1:

- **Allowed**
  - local repo inspection
  - bounded synthesis upward
  - draft issue / PR text in review output

- **Not allowed**
  - direct GitHub issue creation
  - direct PR creation
  - direct branch mutation
  - self-modifying policy or memory changes without admin ratification

## Design intention

This brief exists to help the system become more self-authored without pretending that authorship already exists.

The cron reviewer and durable reviewer should both look at the same living repo and tell us what seems worth changing.
Their disagreement is useful.
Their overlap is useful.
Their blind spots are useful.

The comparison is part of the feature.
