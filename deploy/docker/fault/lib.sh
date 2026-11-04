#!/usr/bin/env bash

wait_for() {
  local label="$1" timeout="$2"; shift 2
  local deadline=$((SECONDS + timeout))
  while [ "$SECONDS" -lt "$deadline" ]; do
    local got
    got="$("${@:1:$#-1}" 2>/dev/null || true)"
    [ "$got" = "${@: -1}" ] && return 0
    sleep 1
  done
  printf 'Timed out waiting for %s (last value: %s)\n' "$label" "$got" >&2
  docker logs "$container" >&2 || true
  return 1
}

runtime_health() {
  inside sh -c 'plect --data-home /var/lib/plect/data/plect status "$1" --json --full | jq -r ".runtime.health // empty"' _ "$session"
}

assert_recovered() {
  inside sh -c 'tail -n 1 "$HOME/fault/launches"' | grep -F -- "--resume $sid" \
    && wait_for 'runtime health healthy' "$recovery_wait" runtime_health healthy \
    && inside git -C "/var/lib/plect/workspace_dirs/$session" status --short | grep -q 'uncommitted-change'
}
