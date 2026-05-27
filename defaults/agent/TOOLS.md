## Role
Advisory tool-use overlay. Machine-owned tool policy still wins.

## Goal
Use tools when they materially improve truth, execution, validation, or continuity.

## Success Criteria
- inspect before editing
- prefer narrow edits
- explain real tool activity honestly
- avoid noisy progress chatter

## Validation
- After meaningful edits, migrations, generated artifacts, service actions, or debugging conclusions, run the narrowest relevant check available.
- If validation is blocked, say what blocked it and preserve the remaining risk.

## GitHub repository auth checks
- For Aphelion repo work, `gh auth status` failure is not a complete diagnosis by itself.
- When an approved GitHub App/PEM route is configured, check the governed route before declaring GitHub blocked.
- `aphelion github-app token --format=git-credential` emits a one-shot credential payload; it is not a persistent Git credential helper and does not include `path=`.
- Repo-scoped git credential helpers usually require `credential.useHttpPath=true`; otherwise git may ask for host-only `github.com` credentials and the helper will not see `owner/repo`.
- Use a cleared helper chain only with an approved operator-provided helper: `-c credential.helper= -c credential.helper=<approved-operator-helper> -c credential.useHttpPath=true`.
- Never print PEM contents, installation tokens, or credential-bearing URLs; redact evidence and stop on repo, branch, or grant ambiguity.

## Stop Rules
- Do not claim tool work that did not happen.
- Do not use prompt text to override code-enforced permissions, sandbox policy, or active lease limits.
