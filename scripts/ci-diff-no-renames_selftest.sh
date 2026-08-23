#!/usr/bin/env bash
# Default rename detection reports only a rename's destination path, so a
# rename across a mapped boundary (e.g. app/x.go -> docs/x.md) would hide
# that the source side lost a file from ci-changed-modules.sh's input.
# --no-renames reports the old and new path as a plain delete+add instead.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mapper="$root/scripts/ci-changed-modules.sh"
workflow="$root/.github/workflows/ci.yml"

fail=0

if ! grep -q 'git diff --no-renames --name-only' "$workflow"; then
  echo "FAIL: ci.yml's PR diff no longer passes --no-renames to git diff" >&2
  fail=1
else
  echo "ok: ci.yml's PR diff passes --no-renames to git diff"
fi

fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
(
  cd "$fixture"
  git init -q
  git config user.email test@example.com
  git config user.name test
  mkdir -p app docs
  echo "package main" > app/source.go
  git add -A && git commit -q -m base
  git mv app/source.go docs/source.md
  git commit -q -m rename
)
base="$(git -C "$fixture" rev-parse HEAD~1)"
head="$(git -C "$fixture" rev-parse HEAD)"

with_renames="$(git -C "$fixture" diff --name-only "$base" "$head")"
if [ "$with_renames" = "$(printf 'docs/source.md')" ]; then
  echo "ok: default rename detection collapses the rename to just the destination (the bug this guards against)"
else
  echo "FAIL: expected default rename detection to only report the destination path; got:" >&2
  printf '%s\n' "$with_renames" | sed 's/^/    /' >&2
  fail=1
fi

no_renames="$(git -C "$fixture" diff --no-renames --name-only "$base" "$head")"
if grep -q '^app/source.go$' <<< "$no_renames" && grep -q '^docs/source.md$' <<< "$no_renames"; then
  echo "ok: --no-renames reports both the old and new path"
else
  echo "FAIL: --no-renames did not report both sides of the rename; got:" >&2
  printf '%s\n' "$no_renames" | sed 's/^/    /' >&2
  fail=1
fi

mapper_out="$(printf '%s\n' "$no_renames" | "$mapper")"
if grep -q '"app"' <<< "$mapper_out"; then
  echo "ok: the preserved source-side path still triggers app's build-test"
else
  echo "FAIL: app's build-test was not triggered by the rename's source-side path; got:" >&2
  printf '%s\n' "$mapper_out" | sed 's/^/    /' >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "cross-scope rename handling regressed." >&2
  exit 1
fi

echo "all cross-scope rename cases passed"
