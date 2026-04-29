# Durable Agent Archetypes

Archetypes are repo-stored templates for creating durable child agents. They are not live agents.

Live child memory, artifacts, grants, snapshots, workspace state, and negotiated runtime identity belong under Aphelion's durable child storage, not under this directory.

Required files:

- `AGENT.md`
- `profile/charter.md`
- `profile/policy.md`
- `profile/capabilities.md`
- `profile/runtime.md`

Optional examples can live under `examples/`.

Use `durable_agent` actions `archetype_list`, `archetype_show`, and `create_from_archetype` to inspect and instantiate templates.
