#!/usr/bin/env bash
# Given a base and head, selects the jobs that diff needs; given none (a push
# to main, or check-all.sh), selects everything. The selection becomes job
# outputs when GITHUB_OUTPUT is set.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/ci-changed-modules_selftest.sh
./scripts/ci-diff-no-renames_selftest.sh

if [ -n "${1:-}" ]; then
  # --no-renames: default rename detection reports only the destination
  # path, so a rename across a mapped boundary (e.g. app/x.go -> docs/x.md)
  # would hide that the source side lost a file.
  out="$(git diff --no-renames --name-only "$1" "$2" | ./scripts/ci-changed-modules.sh)"
else
  out="$(FORCE_FULL_RUN=true ./scripts/ci-changed-modules.sh </dev/null)"
fi
echo "$out"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  for key in FULL_RUN BUILD_TEST_MATRIX INTEGRATION_TEST README_VERIFY AFFECTED_PLUGINS WEB_CI; do
    echo "$(tr '[:upper:]' '[:lower:]' <<<"$key")=$(sed -n "s/^$key=//p" <<<"$out")" >>"$GITHUB_OUTPUT"
  done
fi
