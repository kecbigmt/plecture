#!/usr/bin/env bash
# GitHub Actions workflows: actionlint, with the pinned shellcheck at the
# same severity check-shellcheck.sh uses for each `run:` script. shellcheck
# is reached through shellcheck_report.py, so a suppression directive in a
# run script fails here as it does in a tracked script. No -ignore pattern
# is passed.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${ACTIONLINT_CHECK_ROOT:-$(pwd)}"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
cd "$root"

if [ ! -d .github/workflows ]; then
  echo "actionlint check passed (no workflow directory)"
  exit 0
fi

actionlint="$(lint_tool actionlint)"
shellcheck="$(lint_tool shellcheck)"
# -pyflakes= turns off the Python integration actionlint enables whenever a
# pyflakes happens to be on PATH, so the result never depends on the host.
if ! SHELLCHECK_OPTS='--norc --severity=warning' "$actionlint" \
  -shellcheck "python3 '$scripts_dir/shellcheck_report.py' --stdin '$shellcheck'" \
  -pyflakes=; then
  echo >&2
  echo "actionlint: fix each workflow error above as its message describes." >&2
  echo "$NO_EXCEPTION" >&2
  exit 1
fi
echo "actionlint check passed"
