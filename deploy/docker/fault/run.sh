#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/../../.." && pwd)"
fixture="$repo/deploy/docker/fault"
tmp="$(mktemp -d)"
container=""
volume=""
image=""
cleanup() {
  local status=$?
  if [ -n "$container" ]; then
    if [ "$status" -ne 0 ]; then
      local log_file
      log_file="$(mktemp /tmp/plecture-fault-container.XXXXXX.log)"
      docker logs "$container" > "$log_file" 2>&1 || true
      printf 'Container log: %s\n' "$log_file" >&2
    fi
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
  if [ -n "$volume" ]; then
    docker volume rm -f "$volume" >/dev/null 2>&1 || true
  fi
  if [ -n "$image" ]; then
    docker image rm "$image" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

# shellcheck source=lib.sh
. "$fixture/lib.sh"

health_period="$(sed -n 's/^period *= *"\([0-9]*\)s"$/\1/p' "$fixture/workflow.toml")"
[ -n "$health_period" ] || { echo "No health period in seconds found in $fixture/workflow.toml" >&2; exit 1; }
# Two full health periods let the watchdog observe the loss and the resume
# land; the margin covers a slow launch.
recovery_wait=$((2 * health_period + 10))

source_mode="${PLECT_FAULT_SOURCE:-pushed}"
case "$source_mode" in
  pushed | local) ;;
  *) echo "PLECT_FAULT_SOURCE must be 'pushed' or 'local', got '$source_mode'" >&2; exit 1 ;;
esac

if [ -n "${PLECT_FAULT_BASE_IMAGE:-}" ]; then
  [ "$source_mode" = pushed ] || { echo "PLECT_FAULT_SOURCE=local conflicts with PLECT_FAULT_BASE_IMAGE, which skips the base build" >&2; exit 1; }
  base="$PLECT_FAULT_BASE_IMAGE"
else
  sha="$(git -C "$repo" rev-parse HEAD)"
  if [ "$source_mode" = pushed ]; then
    base="plecture-fault-base:$sha"
    docker build -f "$repo/deploy/docker/Dockerfile" --build-arg "PLECT_SOURCE_SHA=$sha" -t "$base" "$repo"
  else
    # Reusing the base Dockerfile unmodified keeps the tested image from
    # drifting from the shipped one.
    mkdir -p "$tmp/local-source/src"
    git -C "$repo" ls-files -z --cached --others --exclude-standard \
      | while IFS= read -r -d '' f; do
          if [ -e "$repo/$f" ] || [ -L "$repo/$f" ]; then printf '%s\0' "$f"; fi
        done \
      | tar -C "$repo" --null -T - -cf - | tar -C "$tmp/local-source/src" -xf -
    revision="local-$sha"
    [ -z "$(git -C "$repo" status --porcelain)" ] || revision="$revision-dirty"
    base="plecture-fault-base:$revision"
    docker build -f "$repo/deploy/docker/Dockerfile" --build-context "source=$tmp/local-source" \
      --build-arg "PLECT_SOURCE_SHA=$revision" -t "$base" "$repo"
  fi
fi

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$tmp/claude" "$fixture/claude.go"
cp "$fixture"/{Dockerfile,workspace.toml,workflow.toml,entrypoint.sh} "$tmp/"
image="plecture-fault:$RANDOM-$RANDOM"
docker build -f "$tmp/Dockerfile" --build-arg "BASE_IMAGE=$base" -t "$image" "$tmp"

start_container() {
  container="plecture-fault-$RANDOM-$RANDOM"
  docker run -d --name "$container" -v "$volume:/var/lib/plect" "$image" >/dev/null
  wait_for 'docker health' 45 docker inspect --format '{{.State.Health.Status}}' "$container" healthy
}

inside() {
  docker exec -u plect "$container" "$@"
}

plect() {
  inside plect --data-home /var/lib/plect/data/plect "$@"
}

new_session() {
  local label="$1" out
  inside sh -c 'mkdir -p "$HOME/fault" && echo hang > "$HOME/fault/mode"'
  out="$(plect up "fault://$label" --workflow fault_recovery --detach 2>&1)" || { echo "$out" >&2; return 1; }
  session="$(printf '%s\n' "$out" | sed -n 's/^Session: //p' | tail -1)"
  [ -n "$session" ] || { echo "No session name in: $out" >&2; return 1; }
  inside tmux send-keys -t "$session" -- 'turn one' Enter
  wait_for 'stub prompt' 10 inside cat /var/lib/plect/home/fault/prompts 'turn one'
  sid="$(inside sh -c 'basename "$HOME"/.claude/sessions/*.json .json')"
  pid="$(inside jq -r .pid "/var/lib/plect/home/.claude/sessions/$sid.json")"
  inside test -f "/var/lib/plect/workspace_dirs/$session/uncommitted-change"
  inside git -C "/var/lib/plect/workspace_dirs/$session" status --short | grep -q 'uncommitted-change'
}

volume="plecture-fault-$RANDOM-$RANDOM"
docker volume create "$volume" >/dev/null
start_container
new_session one
echo 'PASS: packaged image launches the stub, records a prompt, and keeps an uncommitted change'

if [ "${PLECT_FAULT_RECOVERY:-0}" != 1 ]; then
  echo 'SKIP: S1 and S2 recovery assertions (set PLECT_FAULT_RECOVERY=1)'
  exit 0
fi

inside kill -9 "$pid"
failed=0
if wait_for 'S1 resumed launch' "$recovery_wait" inside sh -c 'wc -l < "$HOME/fault/launches"' 2 \
  && assert_recovered; then
  echo 'PASS: S1 agent loss recovered the same conversation and worktree'
else
  echo 'FAIL: S1 agent loss did not recover the same conversation and worktree' >&2
  failed=1
fi

docker rm -f "$container" >/dev/null
container=""
docker volume rm "$volume" >/dev/null
volume="plecture-fault-$RANDOM-$RANDOM"
docker volume create "$volume" >/dev/null
start_container
new_session two
docker kill "$container" >/dev/null
docker rm "$container" >/dev/null
container=""
start_container
if wait_for 'S2 resumed launch after container replacement' "$recovery_wait" inside sh -c 'wc -l < "$HOME/fault/launches"' 2 \
  && assert_recovered; then
  echo 'PASS: S2 container replacement recovered the same conversation and worktree'
else
  echo 'FAIL: S2 container replacement did not recover the same conversation and worktree' >&2
  failed=1
fi

# S5: a deliberate stop survives a resident crash and a child terminal
# notification, while a sleeping session resumes for its own resource event.
plect down "$session" >/dev/null
docker kill "$container" >/dev/null
docker rm "$container" >/dev/null
container=""
start_container
plect event publish "$session" --type plect.terminal.done --direction inbound --summary 'child completed' >/dev/null
sleep 3
if [ "$(plect status "$session" --json --full | jq -r .runtime.run)" = down ] \
  && [ "$(plect status "$session" --json --full | jq -r '.runtime.sleep // empty')" = "" ]; then
  echo 'PASS: S5 full stop survives container kill and child completion'
else
  echo 'FAIL: S5 full stop woke after container kill or child completion' >&2
  failed=1
fi
plect down "$session" --sleep >/dev/null
plect event publish "$session" --type resource.updated --direction inbound --summary 'resource changed' >/dev/null
if wait_for 'S5 sleeping session wakes for resource event' "$recovery_wait" inside sh -c 'plect --data-home /var/lib/plect/data/plect status "$1" --json --full | jq -r .runtime.run' _ "$session" up; then
  echo 'PASS: S5 sleep wakes for a resource event'
else
  echo 'FAIL: S5 sleep did not wake for a resource event' >&2
  failed=1
fi
exit "$failed"
