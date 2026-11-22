#!/usr/bin/env bash
# The steps come from ci.yml itself, through scripts/ci_steps.py, rather
# than from a list kept here: a hand-kept list let changes pass locally and
# still fail a CI job it never ran.
set -euo pipefail

# CHECK_ALL_ROOT lets check-all_selftest.sh point the scanned/tested tree
# at a small fixture while still invoking the real scripts below (resolved
# from this script's own location, unaffected by the override) — the same
# root-override convention every scripts/check-*.sh here already follows
# for its own selftest.
scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="${CHECK_ALL_ROOT:-$(cd "$scripts_dir/.." && pwd)}"
cd "$root"

base_ref="${1:-}"
if [ -z "$base_ref" ]; then
  base_ref="$(git merge-base HEAD origin/main)"
fi

fail() {
  echo "check-all.sh: FAILED: $1" >&2
  exit 1
}

step() {
  local name="$1"
  shift
  echo "==> $name"
  if ! "$@"; then
    fail "$name"
  fi
}

# CI checks out the committed head, and the diff-scoped steps compare
# commits, so an uncommitted change would be tested by some steps and
# invisible to others. The override keeps a status.showUntrackedFiles=no
# setting from hiding an untracked file.
if [ -n "$(git -c status.showUntrackedFiles=normal status --porcelain)" ]; then
  git -c status.showUntrackedFiles=normal status --short >&2
  fail "uncommitted changes; commit them so every step sees the head CI will run"
fi

# CI installs pinned global tools (npm install -g); a throwaway prefix gives
# the same pinned versions without touching the developer's own globals.
npm_prefix="$(mktemp -d)"
trap 'rm -rf "$npm_prefix"' EXIT
export NPM_CONFIG_PREFIX="$npm_prefix"
export PATH="$npm_prefix/bin:$PATH"

step ".github/workflows/ci.yml jobs required by ci-ok" \
  python3 "$scripts_dir/ci_steps.py" run .github/workflows/ci.yml ci-ok "$base_ref"
step "scripts/check-agent-config.sh" "$scripts_dir/check-agent-config.sh"

echo "check-all.sh: all checks passed"
