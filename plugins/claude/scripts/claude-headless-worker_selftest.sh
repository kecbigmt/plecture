#!/usr/bin/env bash
# Proves the worker's contract with `claude -p` across its real process
# boundary — a stubbed `claude` binary and a background worker process —
# because every property here lives at that boundary:
#
#   1. The first turn starts the conversation under the session id it was
#      handed, and every later turn resumes that same id.
#   2. A first turn that fails before claude reports a session id leaves
#      the next turn starting fresh, not resuming a conversation that was
#      never persisted.
#   3. An api_key_file's content reaches claude as ANTHROPIC_API_KEY, and
#      the key never appears on claude's own command line.
#   4. Terminating the worker mid-turn also terminates the running claude,
#      so a torn-down session stops spending.
#   5. With a socket path, the worker reports ready only once its
#      channel-server queue bridge listens there, relaunches a bridge that
#      dies, and takes the bridge down with it.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
worker="$here/claude-headless-worker"
tmp="$(mktemp -d)"

fail=0
worker_pid=""
cleanup() {
  if [ -n "$worker_pid" ]; then
    kill "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp" 2>/dev/null || { sleep 0.3; rm -rf "$tmp"; }
}
trap cleanup EXIT

bin_dir="$tmp/bin"
mkdir -p "$bin_dir"

# The stub records its argv and the key it was given, then behaves as
# $STUB_MODE (read fresh per invocation, since the worker forks it long
# after this script's own exports) says: "ok" prints a result object
# carrying session_id, "fail-early" exits non-zero without one, and "hang"
# writes its own pid and sleeps until killed.
cat > "$bin_dir/claude" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_ARGV"
printf '%s\n' "${ANTHROPIC_API_KEY-<unset>}" >> "$STUB_KEYS"
mode="$(cat "$STUB_MODE_FILE")"
case "$mode" in
  ok) printf '{"type":"result","is_error":false,"session_id":"stub","result":"done"}\n' ;;
  fail-early) echo "boom" >&2; exit 1 ;;
  hang) printf '%s' "$$" > "$STUB_HANG_PID"; sleep 30 ;;
esac
EOF
chmod +x "$bin_dir/claude"

cat > "$bin_dir/plect" <<'EOF'
#!/usr/bin/env bash
true
EOF
chmod +x "$bin_dir/plect"

export PATH="$bin_dir:$PATH"
export STUB_ARGV="$tmp/argv"
export STUB_KEYS="$tmp/keys"
export STUB_MODE_FILE="$tmp/mode"
export STUB_HANG_PID="$tmp/hang.pid"
export PLECT_SESSION_NAME="selftest/worker-1"
export PLECT_CLAUDE_API_KEY_FILE="$tmp/api-key"
printf 'sk-selftest-key\n' > "$PLECT_CLAUDE_API_KEY_FILE"
: > "$STUB_ARGV"
: > "$STUB_KEYS"

queue_dir="$tmp/queue"
state_dir="$tmp/state"
sid="11111111-2222-3333-4444-555555555555"
mkdir -p "$queue_dir"

"$worker" "$queue_dir" "$state_dir" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!

wait_for_drain() {
  local file="$1"
  for _ in $(seq 1 100); do
    [ -e "$file" ] || return 0
    sleep 0.1
  done
  return 1
}

enqueue() {
  echo "{\"type\":\"user.emit\",\"text\":\"$2\"}" > "$queue_dir/$1.json"
  wait_for_drain "$queue_dir/$1.json" || { echo "FAIL $1 was never drained from the queue" >&2; fail=1; }
}

expect_last_argv() {
  local label="$1" want="$2" got
  got="$(tail -n 1 "$STUB_ARGV")"
  case "$got" in
    *"$want"*) echo "ok   $label" ;;
    *) printf 'FAIL %s: got %q, want it to contain %q\n' "$label" "$got" "$want" >&2; fail=1 ;;
  esac
}

echo fail-early > "$STUB_MODE_FILE"
enqueue t1 "turn one"
expect_last_argv "a first turn starts under the handed session id" "--session-id $sid"

echo ok > "$STUB_MODE_FILE"
enqueue t2 "turn two"
expect_last_argv "a turn after an early failure still starts fresh" "--session-id $sid"

enqueue t3 "turn three"
expect_last_argv "a turn after a reported session resumes it" "--resume $sid"
expect_last_argv "the prompt follows an option terminator" "-- turn three"

if grep -q 'sk-selftest-key' "$STUB_ARGV"; then
  echo "FAIL the api key must never appear on claude's command line" >&2
  fail=1
elif [ "$(tail -n 1 "$STUB_KEYS")" = "sk-selftest-key" ]; then
  echo "ok   api_key_file reaches claude as ANTHROPIC_API_KEY only"
else
  printf 'FAIL ANTHROPIC_API_KEY: got %q\n' "$(tail -n 1 "$STUB_KEYS")" >&2
  fail=1
fi

if jq -e '.last_exit_code == 0' "$state_dir/health.json" >/dev/null 2>&1; then
  echo "ok   health.json records the last turn's exit code"
else
  echo "FAIL health.json after a successful turn: $(cat "$state_dir/health.json" 2>/dev/null)" >&2
  fail=1
fi

echo hang > "$STUB_MODE_FILE"
rm -f "$STUB_HANG_PID"
echo '{"type":"user.emit","text":"turn four"}' > "$queue_dir/t4.json"
for _ in $(seq 1 100); do
  [ -s "$STUB_HANG_PID" ] && break
  sleep 0.1
done
hang_pid="$(cat "$STUB_HANG_PID" 2>/dev/null || true)"
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
gone=0
for _ in $(seq 1 50); do
  kill -0 "$hang_pid" 2>/dev/null || { gone=1; break; }
  sleep 0.1
done
if [ -n "$hang_pid" ] && [ "$gone" -eq 1 ]; then
  echo "ok   terminating the worker mid-turn terminates the running claude"
else
  echo "FAIL claude (pid ${hang_pid:-unknown}) outlived its terminated worker" >&2
  kill -KILL "$hang_pid" 2>/dev/null || true
  fail=1
fi

# The bridge stub records its argv and pid, then stands in for the socket by
# creating the file at --socket, after a delay long enough that a worker not
# waiting for it would report ready first.
cat > "$bin_dir/bridge" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$BRIDGE_ARGV"
printf '%s\n' "$$" >> "$BRIDGE_PIDS"
while [ $# -gt 0 ]; do
  [ "$1" = "--socket" ] && sock="$2"
  shift
done
sleep 0.5
: > "$sock"
exec sleep 30
EOF
chmod +x "$bin_dir/bridge"
export BRIDGE_ARGV="$tmp/bridge-argv"
export BRIDGE_PIDS="$tmp/bridge-pids"
: > "$BRIDGE_ARGV"
: > "$BRIDGE_PIDS"

bridged_state="$tmp/bridged-state"
bridged_queue="$tmp/bridged-queue"
socket="$tmp/bridge.sock"
PLECT_CLAUDE_CHANNEL_SERVER_BIN="$bin_dir/bridge" \
  "$worker" "$bridged_queue" "$bridged_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" "$socket" &
worker_pid=$!

for _ in $(seq 1 50); do
  [ -f "$bridged_state/ready" ] && break
  sleep 0.1
done
if [ -f "$bridged_state/ready" ] && [ -e "$socket" ]; then
  echo "ok   the worker reports ready only once its bridge's socket exists"
else
  echo "FAIL ready=$([ -f "$bridged_state/ready" ] && echo yes || echo no) socket=$([ -e "$socket" ] && echo yes || echo no)" >&2
  fail=1
fi
case "$(head -n 1 "$BRIDGE_ARGV")" in
  "queue --socket $socket --queue-dir $bridged_queue") echo "ok   the bridge writes into the worker's own queue" ;;
  *) printf 'FAIL bridge argv: %q\n' "$(head -n 1 "$BRIDGE_ARGV")" >&2; fail=1 ;;
esac

kill -KILL "$(head -n 1 "$BRIDGE_PIDS")" 2>/dev/null || true
for _ in $(seq 1 50); do
  [ "$(wc -l < "$BRIDGE_PIDS")" -ge 2 ] && break
  sleep 0.1
done
if [ "$(wc -l < "$BRIDGE_PIDS")" -ge 2 ]; then
  echo "ok   a bridge that dies is relaunched"
else
  echo "FAIL a dead bridge was never relaunched" >&2
  fail=1
fi

kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
bridge_pid="$(tail -n 1 "$BRIDGE_PIDS")"
gone=0
for _ in $(seq 1 50); do
  kill -0 "$bridge_pid" 2>/dev/null || { gone=1; break; }
  sleep 0.1
done
if [ "$gone" -eq 1 ]; then
  echo "ok   terminating the worker terminates its bridge"
else
  echo "FAIL bridge (pid $bridge_pid) outlived its terminated worker" >&2
  kill -KILL "$bridge_pid" 2>/dev/null || true
  fail=1
fi

[ "$fail" -eq 0 ] || { echo "claude-headless-worker selftest failed" >&2; exit 1; }
echo "claude-headless-worker selftest passed"
