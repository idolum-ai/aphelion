# Per-Role Model Evaluation

_Status: draft methodology for evidence-backed model selection._

Aphelion should choose models by role evidence, not by model reputation. The
target is the cheapest model that clears the role's quality bar with acceptable
latency and provider reliability. A route that is cheaper but violates authority
or evidence boundaries is not cheaper; it is a failed route.

## Evaluation Shape

The first runnable role is `governor`. Its quality oracle already exists:
canonical, trajectory, boundary-attack, context-fidelity, and cost-fidelity eval
reports. These suites measure the role that can act, propose bounded authority,
recover work, and preserve evidence. The `aphelion eval model-bakeoff` command
wraps those suites and produces a route frontier:

- pass rate, hard failures, ambiguity, and provider failures
- context-fidelity rates for hydration, leaks, and evidence retention
- deterministic prompt-cost shape: estimated prompt tokens, model-call count,
  cache-eligible stable prefixes, and stable-prefix hash stability
- provider-reported usage when live providers return it: input, output,
  total, cache-read, and cache-write tokens
- elapsed time per route/scenario rollout

The command is advisory only. It does not mutate `/model` slots, recipes, or
configuration.

Current runtime defaults should be read as evidence-backed hypotheses:
interactive/recovery governor work defaults to GPT-5.5 with high reasoning on
fresh installs, `/doctor` stays on the strong governor-shaped lane, and
status/heartbeat/curiosity default to cheap lanes (OpenAI mini, Anthropic Haiku,
or OpenRouter Haiku in configured-provider order). Operators can override each
slot through `/model`; clearing a slot returns to the install default. New
defaults should come from a bakeoff report, not reputation or anecdote.

Reasoning effort is part of the governor route frontier when the question is
whether more deliberation buys measurable safety or continuity. Effort-tier
bakeoffs should start on the focused `challenge` suite before spending on the
full model matrix; the challenge slice is composed from existing authority,
continuation, context-fidelity, recovery, and boundary-attack scenarios where
shallow pattern matching is most likely to fail.

## Runnable And Scaffolded Roles

Only a role with a validated quality oracle should be runnable in the bakeoff
pipeline.

| Role | Status | Quality oracle |
| --- | --- | --- |
| `governor` | runnable | canonical, trajectory, boundary-attack, context-fidelity, cost-fidelity |
| `persona` | scaffolded | model phenomenology fixtures exist, but calibrated face judges are still required |
| `doctor` | scaffolded | status/doctor structure exists; readable diagnostic summary quality still needs an oracle |
| `child_default` | scaffolded | durable child policy tests exist; per-child task fixtures are still needed |
| `structured` | scaffolded | each ranker/classifier needs exact-output or deterministic ranking fixtures |

Generative roles need human-rated anchor examples before LLM judges can be
trusted as a model-selection signal. Until then their evals are useful for
scouting failure signatures, not for promoting a default route.

## Ritual

Use deterministic local bakeoffs to prove the reporting machinery:

```sh
aphelion eval model-bakeoff --role governor --mode local \
  --suites canonical,trajectory,boundary_attack --format markdown
```

Use live bakeoffs when the question is provider/model choice:

```sh
aphelion eval model-bakeoff --role governor --mode live \
  --routes openai:gpt-5.5,openai:gpt-5.4,anthropic:claude-sonnet-4-6 \
  --suites canonical,trajectory,boundary_attack --rollouts 3 --jobs 1 \
  --confirm-live-cost --out governor-bakeoff.json
```

Live bakeoffs estimate subject, attacker, and judge provider-call counts before
running. Sweeps above the default live threshold must pass
`--confirm-live-cost`; lower `--live-cost-threshold` for dry-run checks, or set
it to `0` only when an outer budget guard already exists.

Use effort-tier bakeoffs before comparing expensive route matrices:

```sh
aphelion eval model-bakeoff --role governor --mode live \
  --routes openai:gpt-5.5 --efforts low,medium,high \
  --suites challenge --rollouts 3 --jobs 1 \
  --confirm-live-cost --out governor-effort-challenge.json
```

If `high` does not materially beat `low` on `challenge`, do not pay for a full
high-effort matrix. If it does separate, compare the chosen effort against
the current accessible Anthropic tier trio on the same challenge slice before
validating the final frontier on the full suites:

```sh
aphelion eval model-bakeoff --role governor --mode live \
  --routes anthropic:claude-haiku-4-5-20251001,anthropic:claude-sonnet-4-6,anthropic:claude-opus-4-8 \
  --suites challenge --rollouts 3 --jobs 1 \
  --confirm-live-cost --out governor-anthropic-challenge.json
```

If a route wins locally for a role, validate the chosen mix end-to-end before
changing live defaults. Roles are not independent: a cheaper governor can
produce a weaker material floor, which can make a stronger face model look
necessary. The final decision is the full-system behavior, not the sum of local
role optima.

When bakeoff evidence promotes a default, update both the runtime slot defaults
and the operator docs. Existing local runtime recipe files and `/model`
overrides are user-owned state; migrations should not rewrite them silently.
