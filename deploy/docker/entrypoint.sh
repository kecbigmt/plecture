#!/usr/bin/env bash
set -euo pipefail

# Runs as root — the container's default user — so it can fix ownership on
# a freshly attached ECS volume (which mounts root:root by default) before
# the single supervised process drops to the unprivileged `plect` user (see
# service/plect-serve/run). Nothing below this file ever needs root once
# runsvdir has started.
PLECT_UID="${PLECT_UID:-10000}"
PLECT_GID="${PLECT_GID:-10000}"

# Every path here is a persisted-volume path from the runtime layout
# contract (deploy/docker/README.md's Persistence section) except
# XDG_RUNTIME_DIR, handled separately below because it is deliberately NOT
# persisted. A `chown -R` here would re-walk the whole volume — including
# every git worktree under workspace_dirs — on every single boot; these
# directories are chowned non-recursively instead, and new content under
# them inherits correct ownership because it is always created by the
# plect-uid process, never by this root-run script.
for dir in \
  "$HOME" \
  "$XDG_DATA_HOME" \
  "$XDG_STATE_HOME" \
  /var/lib/plect/workspace_dirs \
  /var/lib/plect/codex-exec \
  ; do
  mkdir -p "$dir"
  chown "$PLECT_UID:$PLECT_GID" "$dir"
done

# Recreated every boot, never on the persisted volume: UDS paths (the bus
# socket) are run-scoped, and a stale one from a previous boot must not
# outlive the container that created it.
rm -rf "$XDG_RUNTIME_DIR"
mkdir -p "$XDG_RUNTIME_DIR"
chown "$PLECT_UID:$PLECT_GID" "$XDG_RUNTIME_DIR"
chmod 0700 "$XDG_RUNTIME_DIR"

runsvdir /etc/service &
SUPERVISOR_PID=$!

# ECS sends SIGTERM to PID 1 on task stop. runsvdir does not itself forward
# signals to the services it supervises (runit's own model routes control
# through `sv`, not raw signals) — without this trap, `plect serve` would
# never see the SIGTERM its own signal.NotifyContext expects, and ECS's
# stop timeout would end in a SIGKILL every time, on every deploy and
# every scale-in.
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
