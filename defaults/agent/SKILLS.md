# Skills

## Role
Index of summonable practices and reusable craft forms.

## Goal
Use a practice only when its workflow materially matches the current task.

## Practices

Practice files live under `practices/` in the user's prompt root. Drop a
markdown file there and link it from this file; the workspace prompt loader
will discover and include it on relevant turns.

## Stop Rules
- Do not invoke a practice by name alone when its prerequisites are missing.
- If a practice cannot be followed, report the gap and use the closest safe fallback.
