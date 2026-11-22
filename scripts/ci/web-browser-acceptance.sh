#!/usr/bin/env bash
# The Web UI build that web-browser-acceptance-browser.sh tests against, and
# the vet of the browser-tagged files, which needs no browser.
set -euo pipefail
scripts="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$scripts/install-web-deps.sh"
(cd "$scripts/../../web/app" && pnpm build)
cd "$scripts/../../app"
go vet -tags browser ./internal/webui/...
