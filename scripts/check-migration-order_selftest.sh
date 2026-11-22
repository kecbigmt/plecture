#!/usr/bin/env bash
# Proves check-migration-order.sh fails on each seeded defect and passes
# a clean change, end to end against scratch repositories. The rules' edge
# cases are covered by the Go tests in app/db/cmd/check-migration-order;
# what is proven here is which files the wrapper reads from git.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-migration-order.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git_() {
  git -C "$repo" -c user.email=test@example.invalid -c user.name=test \
    -c commit.gpgsign=false "$@"
}

add() {
  local p
  for p in "$@"; do
    mkdir -p "$repo/$(dirname "$p")"
    printf '%s\n' "$p" >"$repo/$p"
    git_ add "$p"
  done
}

# Every fixture branches "feature" off a main whose latest migration is
# 20261120000000, then runs the checker with main's tip as the base.
new_fixture() {
  repo="$work/$1"
  mkdir -p "$repo"
  git -C "$repo" init -q -b main
  add app/db/migrations/20261120000000_inbox_entries.sql
  git_ commit -qm base
  git_ checkout -q -b feature
}

run_check() {
  (cd "$repo" && MIGRATION_ORDER_CHECK_ROOT="$repo" "$checker" main feature) \
    >"$work/$1.log" 2>&1
}

expect_failure() {
  local name="$1" want="$2"
  if run_check "$name"; then
    echo "FAIL: checker passed the seeded defect: $name" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  if ! grep -qF "$want" "$work/$name.log"; then
    echo "FAIL: checker output for $name does not contain: $want" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  echo "ok: $name"
}

new_fixture misordered
add app/db/migrations/20261121000000_concurrent.sql
git_ commit -qm "migration branched before a newer one merged"
git_ checkout -q main
add app/db/migrations/20261122000000_merged_first.sql
git_ commit -qm "concurrent merge"
expect_failure misordered \
  "app/db/migrations/20261121000000_concurrent.sql:1: migration-sorts-after-base"

new_fixture unversioned
add app/db/migrations/next.sql
git_ commit -qm "migration named without a version"
expect_failure unversioned \
  "app/db/migrations/next.sql:1: migration-has-version"

expect_success() {
  local name="$1"
  if ! run_check "$name"; then
    echo "FAIL: checker failed $name" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  echo "ok: $name passes"
}

new_fixture clean
add app/db/migrations/20261121000000_next.sql
git_ commit -qm "ordered migration with no procedure"
expect_success clean
