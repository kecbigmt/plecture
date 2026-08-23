#!/usr/bin/env bash
# Scopes discovery to the named plugin directories so that one plugin's
# change cannot run another plugin's selftest. REQUIRE_FOUND=true is the
# discovery-canary for a full-repo scan (every plugin named): it catches
# the naming convention silently breaking, which a scoped, legitimately
# selftest-less plugin subset must not be held to.
set -euo pipefail

root="${PLUGIN_SELFTEST_ROOT:-$(pwd)}"

found=0
for name in "$@"; do
  while IFS= read -r t; do
    found=1
    echo "== $t"
    bash "$t"
  done < <(find "$root/plugins/$name" -name '*_selftest.sh' | sort)
done

if [ "${REQUIRE_FOUND:-false}" = true ] && [ "$found" -eq 0 ]; then
  echo "no plugin script selftests found among: $*" >&2
  exit 1
fi
