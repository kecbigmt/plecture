#!/usr/bin/env bash
# AFFECTED_PLUGINS (a JSON array) and REQUIRE_FOUND come from the changes
# job; without them every plugin runs, as on a full run.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/run-plugin-selftests_selftest.sh
if [ -z "${AFFECTED_PLUGINS+set}" ]; then
  AFFECTED_PLUGINS="$(FORCE_FULL_RUN=true ./scripts/ci-changed-modules.sh </dev/null | sed -n 's/^AFFECTED_PLUGINS=//p')"
  export REQUIRE_FOUND=true
fi
mapfile -t names < <(jq -r '.[]' <<<"$AFFECTED_PLUGINS")
./scripts/run-plugin-selftests.sh "${names[@]}"
