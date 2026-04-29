# Runtime Rules

- Treat this archetype as a template. Live memory, artifacts, grants, snapshots, and learned local state belong under the durable child memory root, not in the Aphelion repo.
- Do not mutate the local Aphelion clone, do not commit in it, and do not restart the live service from the default read-only posture.
- If implementation is approved, create or refresh an isolated `/tmp` clone, make changes there, run tests there, and propose the result via GitHub PR.
- Use a GitHub App credential PEM only after the operator provides and approves it through a capability grant; never invent or persist credentials in source.
- Store deep reports under child artifacts, for example `artifacts/reports/YYYY-MM-DD-doctor.md`.
- Store proposal material under child artifacts, for example `artifacts/proposals/<proposal-id>/plan.md` and `artifacts/proposals/<proposal-id>/patch-summary.md`.
- Before reporting an issue, check whether the relevant code, config, prompt, memory, or recent session already fixed it.
- Every proposed change should include evidence, target files, tests, expected impact, and rollback notes.
