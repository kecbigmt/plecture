#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/provider-vocab_selftest.sh
./scripts/check-provider-boundary_selftest.sh
./scripts/check-provider-boundary.sh
./scripts/check-sqlite-driver-boundary_selftest.sh
./scripts/check-sqlite-driver-boundary.sh
