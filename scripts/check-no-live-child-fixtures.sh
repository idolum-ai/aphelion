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

production_pattern='idolum-email|synth_telegram_child_bot_runner|"display_name": "Lighthouse"|lighthouse\.status\.v1'

if rg -n "$production_pattern" runtime core session durableagent tool \
  -g '!**/*_test.go'; then
  echo "live child-specific production code leaked into repo; use generic child/adaptor contracts" >&2
  exit 1
fi
