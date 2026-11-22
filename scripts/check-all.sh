#!/usr/bin/env bash
# It only invokes the existing scripts: duplicating any one check's logic
# here would let this and CI drift apart.
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

echo "==> gofmt -l"
unformatted="$(gofmt -l $(find . -name '*.go' -not -path './app/internal/webui/*'))" || fail "gofmt -l"
if [ -n "$unformatted" ]; then
  echo "$unformatted"
  fail "gofmt -l"
fi

step "go vet ./app/..." bash -c 'cd app && go vet ./...'
step "go test ./app/..." bash -c 'cd app && go test ./...'
step "contracts/state go test ./..." bash -c 'cd contracts/state && go test ./...'

step "scripts/check-provider-boundary.sh" "$scripts_dir/check-provider-boundary.sh"
step "scripts/check-sqlite-driver-boundary.sh" "$scripts_dir/check-sqlite-driver-boundary.sh"
step "scripts/check-comment-references.sh" "$scripts_dir/check-comment-references.sh"
step "scripts/check-comment-density.sh $base_ref HEAD" "$scripts_dir/check-comment-density.sh" "$base_ref" HEAD
step "scripts/check-instruction-orphans.sh" "$scripts_dir/check-instruction-orphans.sh"
step "scripts/check-agent-config.sh" "$scripts_dir/check-agent-config.sh"
step "scripts/check-adr-lifecycle.sh origin/main HEAD" "$scripts_dir/check-adr-lifecycle.sh" origin/main HEAD

# Before the first push there is no pull request, so CI has no body to check
# either; a failed lookup with a remote present still fails, so an outage
# cannot read as a pass.
branch="$(git branch --show-current)"
if ! command -v gh >/dev/null || ! git remote get-url origin >/dev/null 2>&1 || [ -z "$branch" ]; then
  echo "==> scripts/check-pr-body.sh skipped: no gh, origin remote, or branch"
else
  repo="$(gh api 'repos/{owner}/{repo}' --jq .full_name)" || fail "scripts/check-pr-body.sh: repository lookup"
  prs="$(gh api "repos/$repo/pulls?state=open&head=${repo%%/*}:$branch")" || fail "scripts/check-pr-body.sh: pull request lookup"
  if [ "$(jq 'length' <<<"$prs")" -eq 0 ]; then
    echo "==> scripts/check-pr-body.sh skipped: no open pull request for $branch"
  else
    body_dir="$(mktemp -d)"
    trap 'rm -rf "$body_dir"' EXIT
    jq -r '.[0].body // ""' <<<"$prs" >"$body_dir/pr-body.md"
    step "scripts/check-pr-body.sh --repo $repo pr-body.md" "$scripts_dir/check-pr-body.sh" --repo "$repo" "$body_dir/pr-body.md"
  fi
fi

echo "check-all.sh: all checks passed"
