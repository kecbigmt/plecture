#!/usr/bin/env bash
# Dead Go code: staticcheck's U1000, run over every go.work module under a
# build-configuration matrix, so an identifier only a tagged file uses is
# not reported as dead.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${GO_UNUSED_CHECK_ROOT:-$(pwd)}"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
cd "$root"

staticcheck="$(lint_tool staticcheck)"
check_go_directives

out="$(mktemp)"
trap 'rm -f "$out"' EXIT
matrix=""
mapfile -t build_configs < <(go_build_configs)
for tags in "${build_configs[@]}"; do
  matrix+="${tags:-default}: -tags=$tags"$'\n'
done

failed=0
modules=0
while IFS= read -r module; do
  modules=$((modules + 1))
  if ! (cd "$module" && "$staticcheck" -checks U1000 -f json -matrix ./... <<< "$matrix") > "$out"; then
    failed=1
    if [ -s "$out" ]; then
      jq -r --arg root "$root/" \
        '"\(.location.file | ltrimstr($root)):\(.location.line):\(.location.column): \(.code): \(.message)"' \
        "$out"
    else
      echo "$module: staticcheck failed without a diagnostic" >&2
    fi
  fi
done < <(go_workspace_modules)

if [ "$failed" -ne 0 ]; then
  echo >&2
  echo "Dead code (staticcheck U1000): delete each unused identifier above. If only a" >&2
  echo "build configuration missing from this script's matrix uses it, add that" >&2
  echo "configuration to the matrix." >&2
  echo "$NO_EXCEPTION" >&2
  exit 1
fi

echo "go unused check passed ($modules modules)"
