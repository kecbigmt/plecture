#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/../../.." && pwd)"
fixture="$repo/deploy/docker/fault"
tmp="$(mktemp -d)"
container=""
volume=""
cleanup() {
  if [ -n "$container" ]; then
    docker logs "$container" > "$tmp/container.log" 2>&1 || true
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
  if [ -n "$volume" ]; then
    docker volume rm -f "$volume" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

if [ -n "${PLECT_FAULT_BASE_IMAGE:-}" ]; then
  base="$PLECT_FAULT_BASE_IMAGE"
else
  sha="$(git -C "$repo" rev-parse HEAD)"
  base="plecture-fault-base:$sha"
  docker build -f "$repo/deploy/docker/Dockerfile" --build-arg "PLECT_SOURCE_SHA=$sha" -t "$base" "$repo"
fi

go build -o "$tmp/claude" "$fixture/claude.go"
cp "$fixture"/{Dockerfile,workspace.toml,workflow.toml,entrypoint.sh,service-run} "$tmp/"
image="plecture-fault:$RANDOM-$RANDOM"
docker build -f "$tmp/Dockerfile" --build-arg "BASE_IMAGE=$base" -t "$image" "$tmp"

start_container() {
  container="plecture-fault-$RANDOM-$RANDOM"
  docker run -d --name "$container" -v "$volume:/var/lib/plect" "$image" >/dev/null
  wait_for 'docker health' 45 docker inspect --format '{{.State.Health.Status}}' "$container" healthy
}

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

inside() {
  docker exec -u plect "$container" "$@"
}

assert_recovered() {
  inside sh -c 'tail -n 1 "$HOME/fault/launches"' | grep -F -- "--resume $sid" \
    && plect status "$session" --json >/dev/null \
    && inside git -C "/var/lib/plect/workspace_dirs/$session" status --short | grep -q 'uncommitted-change'
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

if [ "${PLECT_FAULT_EXPECT_RED:-0}" != 1 ]; then
  echo 'SKIP: S1 and S2 recovery assertions (set PLECT_FAULT_EXPECT_RED=1)'
  exit 0
fi

inside kill -9 "$pid"
failed=0
if wait_for 'S1 resumed launch' 20 inside sh -c 'wc -l < "$HOME/fault/launches"' 2 \
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
if wait_for 'S2 resumed launch after container replacement' 20 inside sh -c 'wc -l < "$HOME/fault/launches"' 2 \
  && assert_recovered; then
  echo 'PASS: S2 container replacement recovered the same conversation and worktree'
else
  echo 'FAIL: S2 container replacement did not recover the same conversation and worktree' >&2
  failed=1
fi
exit "$failed"
