#!/usr/bin/env bash
# Shell scripts: shellcheck at severity warning over every tracked script,
# found by extension or shebang. Below warning, shellcheck's info and style
# notes are mostly single-quoted jq and awk programs it cannot tell from a
# forgotten expansion.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${SHELLCHECK_CHECK_ROOT:-$(pwd)}"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
cd "$root"

shellcheck="$(lint_tool shellcheck)"
python3 "$scripts_dir/shellcheck_report.py" "$shellcheck"
