#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/check-comment-references_selftest.sh
./scripts/check-comment-references.sh
