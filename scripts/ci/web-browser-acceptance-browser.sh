#!/usr/bin/env bash
# Runs after web-browser-acceptance.sh has built the Web UI it serves.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../../app"

go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install --with-deps chromium
go test -tags browser ./internal/webui/...
