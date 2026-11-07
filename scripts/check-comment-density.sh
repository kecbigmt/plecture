#!/usr/bin/env bash
# Enforces the comment-shape rules CLAUDE.md's Comments section states
# (density, block length, no duplicated rationale), scoped to one PR's
# added/modified lines rather than whole files: a worker should hit this
# before opening the PR, on exactly the lines the PR is responsible for,
# not on pre-existing comments elsewhere in a touched file.
#
# The check logic itself lives in comment_density.py: language lexing (Go,
# TypeScript, SQL, TOML each have their own comment/string syntax) and
# multi-file sentence-duplication tracking are naturally a small stateful
# program, not a shell/awk one-liner, and this repo already keeps
# scripts/provider-vocab.py as a stdlib-only Python helper alongside its
# bash wrapper for the same reason.
set -euo pipefail

root="${COMMENT_DENSITY_CHECK_ROOT:-$(pwd)}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "usage: check-comment-density.sh [--per-commit] <base-ref> <head-ref>" >&2
  exit 2
}

per_commit=0
if [ "${1:-}" = "--per-commit" ]; then
  per_commit=1
  shift
fi
[ "$#" -eq 2 ] || usage

if [ "$per_commit" -eq 0 ]; then
  exec python3 "$script_dir/comment_density.py" "$root" "$1" "$2"
fi

# A branch built from squash-merged sub-PRs fails the aggregate diff by
# construction even when every sub-PR passed, yet skipping it would leave a
# direct commit on the branch (a conflict fix, a merge) unchecked; scoring
# each commit against its first parent checks exactly the reviewed units.
if ! revs="$(git -C "$root" rev-list --reverse "$1..$2")"; then
  echo "check-comment-density.sh: cannot list commits in $1..$2" >&2
  exit 2
fi
commits=()
[ -z "$revs" ] || mapfile -t commits <<<"$revs"
failed=()
for c in "${commits[@]}"; do
  label="$(git -C "$root" log -1 --format='%h %s' "$c")"
  echo "==> $label"
  if ! python3 "$script_dir/comment_density.py" "$root" "$c^1" "$c"; then
    failed+=("$label")
  fi
done

echo "scored ${#commits[@]} commit(s) individually"
if [ "${#failed[@]}" -gt 0 ]; then
  echo "comment-density failed on ${#failed[@]} commit(s):" >&2
  printf '  %s\n' "${failed[@]}" >&2
  exit 1
fi
