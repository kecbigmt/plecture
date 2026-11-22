#!/usr/bin/env bash
# Usage: lint.sh [<base>]. errcheck is scoped to the lines added since the
# base, so it runs only when one is given.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/lint-tools_selftest.sh
./scripts/check-go-unused_selftest.sh
./scripts/check-go-errcheck_selftest.sh
./scripts/check-shellcheck_selftest.sh
./scripts/check-actionlint_selftest.sh
./scripts/check-go-unused.sh
if [ -n "${1:-}" ]; then
  ./scripts/check-go-errcheck.sh "$1"
fi
./scripts/check-shellcheck.sh
./scripts/check-actionlint.sh
