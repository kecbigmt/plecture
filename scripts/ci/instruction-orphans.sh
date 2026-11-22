#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/check-instruction-orphans_selftest.sh
./scripts/check-instruction-orphans.sh
