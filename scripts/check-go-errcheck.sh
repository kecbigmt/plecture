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

failed=0
modules=0
while IFS= read -r module; do
  modules=$((modules + 1))
  if ! (cd "$module" && "$golangci_lint" run \
    --config "$scripts_dir/golangci-errcheck.yml" \
    --new-from-merge-base "$base_ref" \
    --path-prefix "$module" \
    ./...); then
    failed=1
  fi
done < <(go_workspace_modules)

if [ "$failed" -ne 0 ]; then
  echo >&2
  echo "Discarded error (errcheck -blank) on a line this change adds: handle or" >&2
  echo "return each error above." >&2
  echo "$NO_EXCEPTION" >&2
  exit 1
fi

echo "go errcheck check passed ($modules modules, lines added since $base_ref)"
