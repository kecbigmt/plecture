#!/usr/bin/env bash
set -euo pipefail

# Runs as root once per boot, to fix ownership on a freshly attached ECS
# volume, before the supervised process drops to `plect` (see
# service/plect-serve/run).
PLECT_UID="${PLECT_UID:-10000}"
PLECT_GID="${PLECT_GID:-10000}"

# HOME/XDG_*_HOME/XDG_RUNTIME_DIR are environment-controlled, and this
# script runs as root before anything drops privilege — an env override
# (a task definition, a downstream image's ENV) pointing one of them
# outside its expected root must fail loud here rather than let a
# chown/mkdir land somewhere unintended. `realpath -m` resolves `..`
# before the comparison so a traversal can't slip past a literal prefix
# match.
require_under() {
  local resolved
  resolved="$(realpath -m -- "$1")"
  case "$resolved" in
  "$2" | "$2"/*) ;;
  *)
    echo "entrypoint: refusing $1 (resolves to $resolved, outside $2)" >&2
    exit 1
    ;;
  esac
}

# Non-recursive: chowning the whole volume on every boot would re-walk
# every git worktree under workspace_dirs, and content created below these
# dirs is already plect-owned (the plect-uid process is what creates it).
for dir in \
  "$HOME" \
  "$XDG_DATA_HOME" \
  "$XDG_STATE_HOME" \
  /var/lib/plect/workspace_dirs \
  /var/lib/plect/codex-exec \
  ; do
  require_under "$dir" /var/lib/plect
  mkdir -p "$dir"
  chown "$PLECT_UID:$PLECT_GID" "$dir"
done

# Never on the persisted volume, so — unlike the loop above — this always
# starts empty on its own; no rm needed before the mkdir.
require_under "$XDG_RUNTIME_DIR" /run/plect
mkdir -p "$XDG_RUNTIME_DIR"
chown "$PLECT_UID:$PLECT_GID" "$XDG_RUNTIME_DIR"
chmod 0700 "$XDG_RUNTIME_DIR"

runsvdir /etc/service &
SUPERVISOR_PID=$!

# runsvdir does not forward signals to what it supervises (runit routes
# control through `sv`), so without this trap `plect serve` would never
# see the SIGTERM its own signal.NotifyContext expects, and ECS's stop
# timeout would end in a SIGKILL every time.
shutdown() {
  sv down /etc/service/plect-serve >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    sv status /etc/service/plect-serve 2>/dev/null | grep -q '^down' && break
    sleep 0.5
  done
  kill "$SUPERVISOR_PID" 2>/dev/null || true
  exit 0
}
trap shutdown TERM INT

wait "$SUPERVISOR_PID"
