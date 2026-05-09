#!/usr/bin/env bash
set -euo pipefail

pattern='x_idolumai|idolum-x|teortax|LLMEnjoyer|willdepue|doomslide|elder_plinius|latest_public_posts'

if rg -n "$pattern" . \
  -g '!bin/**' \
  -g '!vendor/**' \
  -g '!scripts/check-no-live-child-fixtures.sh'; then
  echo "live child/account fixture leaked into repo; use generic fixture names" >&2
  exit 1
fi
