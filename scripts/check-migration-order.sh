#!/usr/bin/env bash
# Fails a change whose new app/db/migrations/*.sql file does not sort after
# the latest migration on the base branch.
#
# The rules live in app/db/cmd/check-migration-order, under app/db so that
# weakening them passes the same owner gate as a schema change. This wrapper
# is the only part that reads git: it writes each path list NUL-delimited, as
# git prints it, so a file name with any byte in it reaches the checker
# unchanged.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: check-migration-order.sh <base-branch-tip> <head-ref>" >&2
  exit 2
fi
base="$1"
head="$2"

root="${MIGRATION_ORDER_CHECK_ROOT:-$(pwd)}"
app_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../app" && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir "$work/input"

cd "$root"
# --no-renames reports a renamed file as a deletion plus an addition, so a
# migration renamed to a new version is checked as the new migration it is.
git ls-tree -z --name-only "$base" -- app/db/migrations/ >"$work/input/base-migrations"
git diff -z --no-renames --name-only --diff-filter=A "$base...$head" -- app/db/migrations/ >"$work/input/added-migrations"

(cd "$app_dir" && go build -o "$work/check-migration-order" ./db/cmd/check-migration-order)
"$work/check-migration-order" "$work/input"
