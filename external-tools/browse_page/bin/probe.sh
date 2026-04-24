#!/usr/bin/env bash
set -euo pipefail

test -f ".aphelion/external-tools/browse_page/installed"
test -x "./external-tools/browse_page/bin/browse_page.sh"
echo "browse_page fixture ready"
