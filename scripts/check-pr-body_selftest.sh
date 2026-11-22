#!/usr/bin/env bash
# Proves the check-pr-body.sh wrapper builds and runs the real checker end to
# end against seeded defects. The rules' edge cases are scripts/prbody's Go
# tests, not repeated here.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-pr-body.sh"
sha="0123456789abcdef0123456789abcdef01234567"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

expect_pass() {
  local name="$1"
  if ! "$checker" --repo acme/widgets "$work/$name.md" >"$work/$name.log" 2>&1; then
    echo "FAIL: $name: checker rejected a valid body" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  echo "ok: $name passes"
}

expect_fail() {
  local name="$1" want="$2"
  if "$checker" --repo acme/widgets "$work/$name.md" >"$work/$name.log" 2>&1; then
    echo "FAIL: $name: checker accepted a seeded defect" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  if ! grep -qF "$work/$name.md:$want" "$work/$name.log"; then
    echo "FAIL: $name: output does not name $want" >&2
    cat "$work/$name.log" >&2
    exit 1
  fi
  echo "ok: $name fails with $want"
}

cat >"$work/clean.md" <<BODY
## Overview

![Change](https://raw.githubusercontent.com/acme/widgets/$sha/docs/history/changes/1-x/overview.svg)

Closes #1
BODY
expect_pass clean

cat >"$work/no-keyword.md" <<'BODY'
Follow-up to #1, closing #2.
BODY
expect_fail no-keyword "1: closing-keyword"

cat >"$work/branch-image.md" <<'BODY'
## Overview

![Change](https://raw.githubusercontent.com/acme/widgets/main/docs/overview.svg)

Closes #1
BODY
expect_fail branch-image "3: overview-image-sha"

cat >"$work/stale-allow.md" <<'BODY'
Closes #1

<!-- check-pr-body: allow closing-keyword because no issue tracks this hotfix -->
BODY
expect_fail stale-allow "3: stale-allow"

if "$checker" "$work/clean.md" >"$work/usage.log" 2>&1; then
  echo "FAIL: checker ran without --repo" >&2
  exit 1
fi
echo "ok: a missing --repo is a usage error"
