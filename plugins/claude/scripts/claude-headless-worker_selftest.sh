#!/usr/bin/env bash
# Proves the worker's contract with `claude -p` across its real process
# boundary — a stubbed `claude` binary and a background worker process —
# because every property here lives at that boundary:
#
#   1. The first turn starts the conversation under the session id it was
#      handed, and every later turn resumes that same id.
#   2. A first turn that fails before claude reports a session id leaves
#      the next turn starting fresh, not resuming a conversation that was
#      never persisted — and so does a marker some other session id left
#      in the same state dir.
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
printf '%q ' "$@" >> "$STUB_ARGV"
printf '\n' >> "$STUB_ARGV"
printf '%s' "${@: -1}" > "$STUB_PROMPT_FILE"
printf '%s\n' "${ANTHROPIC_API_KEY-<unset>}" >> "$STUB_KEYS"
[ -n "${STUB_HEALTH_FILE:-}" ] && jq -r '.last_exit_code // "missing"' "$STUB_HEALTH_FILE" >> "$STUB_HEALTH_LOG" 2>/dev/null
mode="$(cat "$STUB_MODE_FILE")"
case "$mode" in
  ok)
    printf '{"prompt_id":"%s","last_assistant_message":"done"}' "$PLECT_BATCH_ID" | "$STUB_ACTIVITY" finish
    printf '{"type":"result","is_error":false,"session_id":"stub","result":"done"}\n'
    ;;
  fail-early) echo "boom" >&2; exit 1 ;;
  hang) printf '%s' "$$" > "$STUB_HANG_PID"; sleep 30 ;;
esac
EOF
chmod +x "$bin_dir/claude"

cat > "$bin_dir/plect" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PLECT_CALLS"
[ "$1 $2" = "state set-message" ] && [ "$4" = "working" ] && [ -f "$PLECT_FAIL_WORKING_ONCE" ] && { rm -f "$PLECT_FAIL_WORKING_ONCE"; exit 1; }
[ "$1 $2" = "state set-message" ] && [ "$4" = "working" ] && [ -f "$PLECT_FAIL_WORKING" ] && exit 1
[ "$1 $2" = "event publish" ] && [ -f "$PLECT_FAIL_PUBLISH" ] && exit 1
true
EOF
chmod +x "$bin_dir/plect"

export PATH="$bin_dir:$PATH"
export STUB_ARGV="$tmp/argv"
export STUB_KEYS="$tmp/keys"
export STUB_MODE_FILE="$tmp/mode"
export STUB_HANG_PID="$tmp/hang.pid"
export STUB_PROMPT_FILE="$tmp/prompt"
export STUB_ACTIVITY="$here/claude-agent-activity"
export STUB_HEALTH_FILE=""
export STUB_HEALTH_LOG="$tmp/health-observed"
export PLECT_CALLS="$tmp/plect-calls"
export PLECT_FAIL_WORKING="$tmp/fail-working"
export PLECT_FAIL_WORKING_ONCE="$tmp/fail-working-once"
export PLECT_FAIL_PUBLISH="$tmp/fail-publish"
export PLECT_SESSION_NAME="selftest/worker-1"
export PLECT_CLAUDE_API_KEY_FILE="$tmp/api-key"
printf 'sk-selftest-key\n' > "$PLECT_CLAUDE_API_KEY_FILE"
: > "$STUB_ARGV"
: > "$STUB_KEYS"
: > "$PLECT_CALLS"

queue_dir="$tmp/queue"
state_dir="$tmp/state"
sid="11111111-2222-3333-4444-555555555555"
mkdir -p "$queue_dir"

mkdir -p "$state_dir"
printf '%s' "99999999-8888-7777-6666-555555555555" > "$state_dir/started"

"$worker" "$queue_dir" "$state_dir" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!

wait_for_drain() {
  local file="$1"
  for _ in $(seq 1 100); do
    [ ! -e "$file" ] && [ ! -d "$(dirname "$file")/.inflight" ] && return 0
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
echo '{"type":"user.emit","text":"turn one"}' > "$queue_dir/t1.json"
for _ in $(seq 1 100); do
  jq -e '.last_exit_code == 1' "$state_dir/health.json" >/dev/null 2>&1 && break
  sleep 0.1
done
expect_last_argv "a first turn starts under the handed session id despite another id's marker" "--session-id $sid"

echo ok > "$STUB_MODE_FILE"
wait_for_drain "$queue_dir/t1.json" || { echo "FAIL failed turn was not retried" >&2; fail=1; }
enqueue t2 "turn two"
expect_last_argv "a turn after an early failure resumes the completed conversation" "--resume $sid"

enqueue t3 "turn three"
expect_last_argv "a turn after a reported session resumes it" "--resume $sid"
expect_last_argv "the batch prompt follows an option terminator" "Consider and answer every numbered entry"
jq -n --arg text $'end\n\n' '{type:"user.emit", text:$text}' > "$queue_dir/t-nl.json"
wait_for_drain "$queue_dir/t-nl.json" || { echo "FAIL trailing-newline input was not delivered" >&2; fail=1; }
if tail -c 7 "$STUB_PROMPT_FILE" | cmp -s - <(printf 'end\n\n\n\n'); then
  echo "ok   the last message's trailing newlines reach the agent"
else
  echo "FAIL the last message's trailing newlines were stripped" >&2
  fail=1
fi

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

echo ok > "$STUB_MODE_FILE"
"$worker" "$queue_dir" "$state_dir" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!
wait_for_drain "$queue_dir/t4.json" || { echo "FAIL interrupted turn was not retried" >&2; fail=1; }
if [ "$(grep -c 'turn four' "$STUB_ARGV" || true)" -eq 2 ]; then
  echo "ok   an interrupted batch is retried and then acknowledged once"
else
  echo "FAIL interrupted batch invocation count: $(grep -c 'turn four' "$STUB_ARGV" || true)" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

: > "$STUB_ARGV"
priority_queue="$tmp/priority-queue"
priority_state="$tmp/priority-state"
mkdir -p "$priority_queue"
echo '{"type":"user.emit","text":"leftover turn"}' > "$priority_queue/1-leftover.json"
echo '{"type":"plect.session.resumed","text":"resume instruction"}' > "$priority_queue/2-resumed.json"
echo '{"type":"user.emit","text":"later turn"}' > "$priority_queue/3-later.json"
"$worker" "$priority_queue" "$priority_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$priority_state/ready" ] && break
  sleep 0.01
done
[ -e "$priority_state/ready" ] || { echo "FAIL resumed worker was never ready" >&2; fail=1; }
wait_for_drain "$priority_queue/3-later.json" || { echo "FAIL queued turns were never drained" >&2; fail=1; }
batch_args="$(cat "$STUB_ARGV")"
if [ "$(grep -c -- 'Consider and answer every numbered entry' "$STUB_ARGV" || true)" -eq 1 ] &&
   [[ "$batch_args" == *"leftover turn"*"resume instruction"*"later turn"* ]]; then
  echo "ok   pending entries form one turn in queue order"
else
  printf 'FAIL queued batch: %s\n' "$(cat "$STUB_ARGV")" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

timeout_queue="$tmp/timeout-queue"
timeout_state="$tmp/timeout-state"
mkdir -p "$timeout_queue"
echo '{"type":"user.emit","text":"leftover without notice"}' > "$timeout_queue/1-leftover.json"
: > "$STUB_ARGV"
"$worker" "$timeout_queue" "$timeout_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$timeout_state/ready" ] && break
  sleep 0.01
done
[ -e "$timeout_state/ready" ] || { echo "FAIL fallback worker was never ready" >&2; fail=1; }
wait_for_drain "$timeout_queue/1-leftover.json" || { echo "FAIL leftover turn was never drained without a notice" >&2; fail=1; }
if [ "$(grep -c -- 'Consider and answer every numbered entry' "$STUB_ARGV" || true)" -eq 1 ] && grep -q 'leftover without notice' "$STUB_ARGV"; then
  echo "ok   a queued turn runs once without a resume instruction"
else
  echo "FAIL queued turn did not run once" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

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

blocked_queue="$tmp/blocked-queue"
blocked_state="$tmp/blocked-state"
mkdir -p "$blocked_queue"
echo '{"type":"user.emit","text":"blocked turn"}' > "$blocked_queue/1.json"
: > "$STUB_ARGV"
touch "$PLECT_FAIL_WORKING"
"$worker" "$blocked_queue" "$blocked_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/blocked.out" 2>&1 &
worker_pid=$!
sleep 7
if [ -s "$STUB_ARGV" ] || [ ! -e "$blocked_queue/.inflight/1.json" ] || ! grep -q 'working' "$tmp/blocked.out"; then
  echo "FAIL a failed working status write started or lost a turn without reporting failure" >&2
  fail=1
else
  echo "ok   a failed working status write leaves the turn queued and reports failure"
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
rm -f "$PLECT_FAIL_WORKING"

retry_queue="$tmp/retry-queue"
retry_state="$tmp/retry-state"
mkdir -p "$retry_queue"
echo '{"type":"user.emit","text":"retry turn"}' > "$retry_queue/1.json"
echo hang > "$STUB_MODE_FILE"
rm -f "$STUB_HANG_PID"
touch "$PLECT_FAIL_WORKING_ONCE"
"$worker" "$retry_queue" "$retry_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/retry.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -s "$STUB_HANG_PID" ] && break
  sleep 0.1
done
if [ ! -s "$STUB_HANG_PID" ] || ! jq -e '.last_exit_code == 0' "$retry_state/health.json" >/dev/null 2>&1; then
  echo "FAIL health probe is not passing during a turn after a retried working write" >&2
  fail=1
else
  echo "ok   health probe passes during a turn after a retried working write"
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

publish_queue="$tmp/publish-queue"
publish_state="$tmp/publish-state"
mkdir -p "$publish_queue"
echo '{"id":"publish","type":"example","text":"publish failure"}' > "$publish_queue/1.json"
echo ok > "$STUB_MODE_FILE"
: > "$STUB_ARGV"
: > "$PLECT_CALLS"
touch "$PLECT_FAIL_PUBLISH"
"$worker" "$publish_queue" "$publish_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!
for _ in $(seq 1 100); do
  grep -q '^event publish' "$PLECT_CALLS" && jq -e '.last_exit_code == 1' "$publish_state/health.json" >/dev/null 2>&1 && break
  sleep 0.1
done
if ! jq -e '.last_exit_code == 1' "$publish_state/health.json" >/dev/null 2>&1 || [ ! -f "$publish_queue/.inflight/1.json" ]; then
  echo "FAIL a reply publication failure acknowledged the batch: health=$(cat "$publish_state/health.json" 2>/dev/null) calls=$(cat "$PLECT_CALLS")" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
rm -f "$PLECT_FAIL_PUBLISH"
"$worker" "$publish_queue" "$publish_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" &
worker_pid=$!
wait_for_drain "$publish_queue/1.json" || { echo "FAIL reply publication was not retried" >&2; fail=1; }
if [ "$(grep -c 'publish failure' "$STUB_ARGV" || true)" -ne 1 ]; then
  echo "FAIL a publish retry invoked the agent again" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

bounded_queue="$tmp/bounded-queue"
bounded_state="$tmp/bounded-state"
mkdir -p "$bounded_queue"
echo '{"id":"poison","type":"example","text":"poison turn"}' > "$bounded_queue/1.json"
: > "$STUB_ARGV"
: > "$STUB_HEALTH_LOG"
export STUB_HEALTH_FILE="$bounded_state/health.json"
echo fail-early > "$STUB_MODE_FILE"
"$worker" "$bounded_queue" "$bounded_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/bounded.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 300); do
  compgen -G "$bounded_queue/.failed-*" >/dev/null && break
  sleep 0.1
done
if ! compgen -G "$bounded_queue/.failed-*" >/dev/null || [ "$(grep -c 'poison turn' "$STUB_ARGV" || true)" -ne 3 ]; then
  echo "FAIL a deterministic agent failure was not parked after three attempts: argv=$(grep -c 'poison turn' "$STUB_ARGV" || true) output=$(cat "$tmp/bounded.out")" >&2
  fail=1
fi
if [ "$(sed -n '2,3p' "$STUB_HEALTH_LOG" | grep -c '^0$' || true)" -ne 2 ]; then
  echo "FAIL health did not recover before each agent retry: $(cat "$STUB_HEALTH_LOG")" >&2
  fail=1
fi
echo ok > "$STUB_MODE_FILE"
echo '{"id":"later","type":"example","text":"later turn"}' > "$bounded_queue/2.json"
wait_for_drain "$bounded_queue/2.json" || { echo "FAIL a parked batch blocked newer work" >&2; fail=1; }
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
export STUB_HEALTH_FILE=""

optout_queue="$tmp/optout-queue"
optout_state="$tmp/optout-state"
mkdir -p "$optout_queue"
echo '{"id":"optout","type":"example","text":"private reply"}' > "$optout_queue/1.json"
: > "$STUB_ARGV"
: > "$PLECT_CALLS"
PLECT_CLAUDE_REPORT_REPLY=false "$worker" "$optout_queue" "$optout_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/optout.out" 2>&1 &
worker_pid=$!
wait_for_drain "$optout_queue/1.json" || { echo "FAIL opted-out batch was not acknowledged: $(cat "$tmp/optout.out")" >&2; fail=1; }
if grep -q '^event publish ' "$PLECT_CALLS" || [ "$(grep -c 'private reply' "$STUB_ARGV" || true)" -ne 1 ]; then
  echo "FAIL opted-out batch published a reply or reran its agent" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

reply_queue="$tmp/reply-queue"
reply_state="$tmp/reply-state"
mkdir -p "$reply_queue"
echo '{"id":"reply","type":"example","text":"stuck publication"}' > "$reply_queue/1.json"
: > "$STUB_ARGV"
touch "$PLECT_FAIL_PUBLISH"
"$worker" "$reply_queue" "$reply_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/reply.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 300); do
  compgen -G "$reply_queue/.failed-*" >/dev/null && break
  sleep 0.1
done
if ! compgen -G "$reply_queue/.failed-*" >/dev/null || [ "$(grep -c 'stuck publication' "$STUB_ARGV" || true)" -ne 1 ]; then
  echo "FAIL repeated publication failures reran the agent or never parked: argv=$(grep -c 'stuck publication' "$STUB_ARGV" || true) output=$(cat "$tmp/reply.out")" >&2
  fail=1
fi
if ! compgen -G "$reply_queue/.failed-*/reply.json" >/dev/null; then
  echo "FAIL the parked batch lost its captured reply" >&2
  fail=1
fi
rm -f "$PLECT_FAIL_PUBLISH"
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

ack_queue="$tmp/ack-queue"
ack_state="$tmp/ack-state"
mkdir -p "$ack_queue/.inflight"
echo '{"id":"done","type":"example","text":"already published"}' > "$ack_queue/.inflight/1.json"
printf '1.json\n' > "$ack_queue/.inflight/manifest"
printf 'completed-batch' > "$ack_queue/.inflight/batch-id"
touch "$ack_queue/.inflight/reply-published"
: > "$STUB_ARGV"
"$worker" "$ack_queue" "$ack_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/ack.out" 2>&1 &
worker_pid=$!
wait_for_drain "$ack_queue/1.json" || { echo "FAIL completed batch was not acknowledged: $(cat "$tmp/ack.out")" >&2; find "$ack_queue" -maxdepth 2 -type f >&2; fail=1; }
if [ -s "$STUB_ARGV" ]; then
  echo "FAIL already published batch invoked the agent again" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

corrupt_queue="$tmp/corrupt-queue"
corrupt_state="$tmp/corrupt-state"
mkdir -p "$corrupt_queue"
printf '{not json\n' > "$corrupt_queue/1.json"
: > "$STUB_ARGV"
"$worker" "$corrupt_queue" "$corrupt_state" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" > "$tmp/corrupt.out" 2>&1 &
worker_pid=$!
wait_for_drain "$corrupt_queue/1.json" || { echo "FAIL malformed-only batch was not acknowledged" >&2; fail=1; }
if [ -s "$STUB_ARGV" ] || ! compgen -G "$corrupt_queue.quarantine/*/1.json" >/dev/null; then
  echo "FAIL malformed-only batch invoked the agent or lost its entry" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

bash "$here/../../headless-queue-conformance.sh" "$here/headless-batch.sh" || fail=1
[ "$fail" -eq 0 ] || { echo "claude-headless-worker selftest failed" >&2; exit 1; }
echo "claude-headless-worker selftest passed"
