#!/usr/bin/env bash
# Dead Go code: staticcheck's U1000, run over every go.work module under a
# build-configuration matrix, so an identifier only a tagged file uses is
# not reported as dead. U1000 takes no exception, and the exception guard
# that runs first rejects any directive that would hide one.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${GO_UNUSED_CHECK_ROOT:-$(pwd)}"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
cd "$root"

staticcheck="$(lint_tool staticcheck)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

(cd "$scripts_dir/../app" && go build -o "$work/check-lint-directives" ./cmd/check-lint-directives)
mapfile -t go_files < <(git ls-files -- '*.go')
if ! "$work/check-lint-directives" "${go_files[@]}"; then
  echo >&2
  echo "Lint exception guard failed: each line above names the rule and its fix." >&2
  exit 1
fi

matrix='default:
integration: -tags=integration
browser: -tags=browser'

failed=0
modules=0
while IFS= read -r module; do
  modules=$((modules + 1))
  if ! (cd "$module" && "$staticcheck" -checks U1000 -f json -matrix ./... <<< "$matrix") > "$work/out.json"; then
    failed=1
    if [ -s "$work/out.json" ]; then
      jq -r --arg root "$root/" \
        '"\(.location.file | ltrimstr($root)):\(.location.line):\(.location.column): \(.code): \(.message)"' \
        "$work/out.json"
    else
      echo "$module: staticcheck failed without a diagnostic" >&2
    fi
  fi
done < <(go_workspace_modules)

if [ "$failed" -ne 0 ]; then
  echo >&2
  echo "Dead code (staticcheck U1000): delete each unused identifier above. If only a" >&2
  echo "build configuration missing from this script's matrix uses it, add that" >&2
  echo "configuration to the matrix. U1000 takes no exception." >&2
  exit 1
fi

echo "go unused check passed ($modules modules, ${#go_files[@]} Go files scanned for exceptions)"
