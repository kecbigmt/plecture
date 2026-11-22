#!/usr/bin/env bash
# Usage: check-all.sh [<head>]. Given a head, the ADR immutability check
# compares it against main, since an ADR on an unmerged base PR is still a
# proposal; without one, only the whole-repo checks run.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/check-all_selftest.sh
./scripts/check-ci-scripts_selftest.sh
./scripts/check-ci-scripts.sh
./scripts/check-adr-lifecycle_selftest.sh
if [ -n "${1:-}" ]; then
  ./scripts/check-adr-lifecycle.sh origin/main "$1"
else
  ./scripts/check-adr-lifecycle.sh
fi
