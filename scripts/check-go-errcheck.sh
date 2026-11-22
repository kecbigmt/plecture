#!/usr/bin/env bash
# Discarded Go errors, gated on the lines a change adds rather than the whole
# tree: the tree holds sites that predate this check, and each is fixed as
# the change that touches it is reviewed. Once a whole-tree run reports
# zero, drop --new-from-merge-base so the check covers every line.
#
# Usage: check-go-errcheck.sh <base-ref>
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: check-go-errcheck.sh <base-ref>" >&2
  exit 2
fi
base_ref="$1"

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${GO_ERRCHECK_CHECK_ROOT:-$(pwd)}"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
cd "$root"

golangci_lint="$(lint_tool golangci-lint)"
check_go_directives

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

# golangci-lint analyzes one build configuration per run, so each is run in
# turn and a site every configuration compiles is reported once.
mapfile -t build_configs < <(go_build_configs)
modules=0
while IFS= read -r module; do
  modules=$((modules + 1))
  for tags in "${build_configs[@]}"; do
    status=0
    (cd "$module" && "$golangci_lint" run \
      --config "$scripts_dir/golangci-errcheck.yml" \
      --build-tags "$tags" \
      --new-from-merge-base "$base_ref" \
      --output.json.path stdout \
      ./...) > "$out/run.json" 2> "$out/run.err" || status=$?
    if [ "$status" -gt 1 ]; then
      echo "$module (tags: ${tags:-none}): golangci-lint failed with status $status:" >&2
      cat "$out/run.err" >&2
      exit 1
    fi
    jq -rn --arg module "$module" \
      'input | .Issues[]? | "\($module)/\(.Pos.Filename):\(.Pos.Line):\(.Pos.Column): \(.Text) (\(.FromLinter))"' \
      "$out/run.json" >> "$out/findings"
  done
done < <(go_workspace_modules)

if [ -s "$out/findings" ]; then
  sort -u -t: -k1,1 -k2,2n -k3,3n "$out/findings"
  echo >&2
  echo "Discarded error (errcheck -blank) on a line this change adds: handle or" >&2
  echo "return each error above." >&2
  echo "$NO_EXCEPTION" >&2
  exit 1
fi

echo "go errcheck check passed ($modules modules, lines added since $base_ref)"
