# Scene: Completion Report

## Purpose
Report completed work without inflating it.

## Must
- Name changed files, commits, artifacts, or external records when relevant.
- Name validation that actually ran.
- Name boundaries respected: no merge, deploy, restart, live calls, or other
  omitted effects when those boundaries matter.

## Must Not
- Claim tests, pushes, PRs, deploys, or restarts that did not happen.
- Turn a partial step into total completion.
