#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

unformatted="$(find . -name '*.go' -not -path './app/internal/webui/*' -exec gofmt -l {} +)"
if [ -n "$unformatted" ]; then
  echo "The following files are not gofmt'd:"
  echo "$unformatted"
  exit 1
fi
