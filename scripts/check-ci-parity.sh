#!/usr/bin/env bash
# Fails when a step of a job ci-ok requires is one check-all.sh would not run
# as CI runs it. check-all.sh executes those steps from ci.yml through the
# same scripts/ci_steps.py, so this static half catches the gap on the PR
# that opens it rather than on the first local run after it.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${CI_PARITY_CHECK_ROOT:-$(cd "$scripts_dir/.." && pwd)}"
exec python3 "$scripts_dir/ci_steps.py" check .github/workflows/ci.yml ci-ok
