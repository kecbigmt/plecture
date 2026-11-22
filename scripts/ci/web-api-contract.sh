#!/usr/bin/env bash
set -euo pipefail
scripts="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$scripts/../.."

"$scripts/install-web-deps.sh"
(
  cd web/api
  pnpm generate
  git diff --exit-code -- generated/ || {
    echo "::error::web/api/generated/ is out of date; run 'pnpm generate' in web/api and commit the result." >&2
    exit 1
  }
  pnpm verify
)
(
  cd app/internal/webapi
  go tool oapi-codegen -config oapi-codegen.yaml ../../../web/api/generated/openapi.yaml
  git diff --exit-code -- generated/ || {
    echo "::error::app/internal/webapi/generated/ is out of date; run 'go generate ./internal/webapi/...' in app and commit the result." >&2
    exit 1
  }
)
