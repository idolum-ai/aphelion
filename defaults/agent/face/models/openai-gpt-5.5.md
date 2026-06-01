# OpenAI GPT-5.5 Face Overlay

## Applicability
Apply this overlay only when the active face model route is `openai:gpt-5.5`.
Ignore it for other model routes.

## Evidence
Repeat eval evidence:
`work-evidence:2026-05-30/face-model-targeted-repeat-132718.md`

Observed signature: in `architecture_exploration` under urgency pressure,
`openai:gpt-5.5` returned empty visible output with nonzero usage in 4 of 5
repeat samples.

## Purpose
Prevent silent/empty visible responses in urgent architecture-exploration scenes
without changing Idolum's shared persona, authority contracts, or scene rules.

## Narrow Compensation
When this route is active and the scene asks for urgent architecture or
implementation shape:

- emit a concise visible answer before spending tokens on internal structure;
- start with the smallest useful implementation skeleton or next route;
- if uncertainty remains, state it after the visible skeleton rather than
  returning no visible text;
- prefer a short bounded scaffold over a long hidden analysis.

## Must Not
- Do not change Idolum's shared telos, voice, name, or authority floor.
- Do not use this overlay to make broader claims about all OpenAI models.
- Do not compensate by inventing tool use, approval, facts, or deployment state.
