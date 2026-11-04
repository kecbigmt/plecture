#!/usr/bin/env bash
# Proves assert_recovered rejects a session that resumed but is not healthy,
# and each other recovery condition, without needing Docker.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lib="${FAULT_LIB:-$here/lib.sh}"
. "$lib"

container=stub session=fault/one+fault_recovery sid=sid-1 recovery_wait=2
resumed_launch="--resume sid-1"
health=healthy
git_status="?? uncommitted-change"
notices=1

docker() { :; }
plect() { :; }
inside() {
  case "$*" in
    *launches*) printf '%s\n' "$launch" ;;
    *prompts*) printf '%s\n' "$notices" ;;
    *"plect status"*) printf '%s\n' "$health" ;;
    git*) printf '%s\n' "$git_status" ;;
  esac
}

failures=0
expect() {
  local want="$1" name="$2" got=fail
  if assert_recovered >/dev/null 2>&1; then got=pass; fi
  if [ "$got" = "$want" ]; then
    echo "ok: $name"
  else
    echo "FAIL: $name (want $want, got $got)" >&2
    failures=$((failures + 1))
  fi
}

launch="$resumed_launch"
expect pass 'resumed, healthy, change kept is accepted'

health=unhealthy
expect fail 'resumed but unhealthy is rejected'

health=""
expect fail 'resumed but health missing is rejected'

health=healthy
launch="--session-id sid-1"
expect fail 'healthy but not resumed is rejected'

launch="$resumed_launch"
git_status=""
expect fail 'resumed and healthy but uncommitted change lost is rejected'

git_status="?? uncommitted-change"
notices=0
expect fail 'resumed and healthy but no resume notice arrived is rejected'

notices=2
expect fail 'resumed and healthy but the resume notice arrived twice is rejected'

[ "$failures" -eq 0 ]
