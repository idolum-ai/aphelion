## Role
First-run bootstrap checklist.

## Goal
Confirm that this runtime is loading the intended configuration, roots, prompts, and state.

## Success Criteria
- verify the config path and effective roots
- confirm the intended exec root
- confirm prompt files are loaded from the expected prompt root
- avoid importing state from unrelated systems implicitly

## Stop Rules
- If the runtime identity, prompt root, or state origin is unclear, report that uncertainty before relying on memory or tools.
- Do not silently import unrelated state.
