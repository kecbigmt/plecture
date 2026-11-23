#!/usr/bin/env bash
# Usage: check-all.sh [<base>]
#
# Runs the scripts/ci/ script of every job ci-ok requires, for every module
# rather than a diff's selection.
set -euo pipefail

scripts_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$scripts_dir/.."

# CI's pull_request base sha is the base branch's tip, not the merge-base.
base_ref="${1:-origin/main}"

fail() {
  echo "check-all.sh: FAILED: $1" >&2
  exit 1
}

step() {
  local name="scripts/${1#"$scripts_dir"/}"
  echo "==> $name ${*:2}"
  "$@" || fail "$name"
}

# The scripts that compare commits see only what is committed, so an
# uncommitted change would be tested by some scripts and invisible to
# others. The override keeps a status.showUntrackedFiles=no setting from
# hiding an untracked file.
if [ -n "$(git -c status.showUntrackedFiles=normal status --porcelain)" ]; then
  git -c status.showUntrackedFiles=normal status --short >&2
  fail "uncommitted changes; commit them so every script sees the head CI will run"
fi
# CI installs pinned global tools (npm install -g); a throwaway prefix
# gives the same pinned versions without touching the developer's own.
npm_prefix="$(mktemp -d)"
trap 'rm -rf "$npm_prefix"' EXIT
export NPM_CONFIG_PREFIX="$npm_prefix"
export PATH="$npm_prefix/bin:$PATH"

modules="$(FORCE_FULL_RUN=true "$scripts_dir/ci-changed-modules.sh" </dev/null | sed -n 's/^BUILD_TEST_MATRIX=//p')"
mapfile -t modules < <(jq -r '.[]' <<<"$modules")

step "$scripts_dir/ci/changes.sh"
step "$scripts_dir/ci/gofmt.sh"
step "$scripts_dir/ci/go-install.sh"
step "$scripts_dir/ci/provider-boundary.sh"
step "$scripts_dir/ci/comment-references.sh"
HEAD_REF="$(git branch --show-current)" step "$scripts_dir/ci/comment-density.sh" "$base_ref" HEAD
step "$scripts_dir/ci/instruction-orphans.sh"
step "$scripts_dir/ci/lint.sh" "$base_ref"
step "$scripts_dir/ci/check-all.sh" HEAD
step "$scripts_dir/ci/gh-guard.sh"
step "$scripts_dir/ci/web-api-contract.sh"
step "$scripts_dir/ci/web-app-build.sh"
# Not web-browser-acceptance-browser.sh, the job's browser half: its
# playwright install --with-deps installs system packages as root, which a
# check must not do to a developer's machine.
step "$scripts_dir/ci/web-browser-acceptance.sh"
step "$scripts_dir/ci/plugin-script-selftests.sh"
step "$scripts_dir/ci/storage-contract.sh" origin/main HEAD
for module in "${modules[@]}"; do
  step "$scripts_dir/ci/build-test.sh" "$module"
done
step "$scripts_dir/ci/integration-test.sh"
step "$scripts_dir/ci/readme-verify.sh"
step "$scripts_dir/check-agent-config.sh"

echo "check-all.sh: all checks passed"
