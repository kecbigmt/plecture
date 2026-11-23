#!/usr/bin/env bash
# Fails when a job ci-ok requires runs a command check-all.sh cannot run as
# CI runs it: every such job is setup actions followed by scripts/ci/*.sh
# calls, and check-all.sh calls each of those scripts.
set -euo pipefail
scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lint-tools.sh
. "$scripts_dir/lint-tools.sh"
yq="$(lint_tool yq)"
cd "${CI_SCRIPTS_CHECK_ROOT:-$(cd "$scripts_dir/.." && pwd)}"
exec python3 "$scripts_dir/check_ci_scripts.py" "$yq" .github/workflows/ci.yml ci-ok scripts/check-all.sh
