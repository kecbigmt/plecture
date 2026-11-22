#!/usr/bin/env bash
set -euo pipefail
scripts="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$scripts/install-web-deps.sh"
cd "$scripts/../../web/app"
pnpm test
pnpm build
