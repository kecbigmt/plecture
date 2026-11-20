#!/usr/bin/env bash
# Proves two properties across the worker's real process boundary — a
# stubbed `codex` binary and a background worker process, not a unit test
# of publish_reply in isolation — because both regressions this guards
# live at that boundary: a shell capture that loses bytes, and a restart
# that resets state a real restart must not.
#
#   1. A turn's captured reply reaches plect.message byte for byte,
#      trailing newlines included.
#   2. message_id keeps advancing across a codex-agent-activity reset —
#      the same reset exec_runtime's setup calls on every launch, resumes
#      included — rather than reusing an id an earlier turn already
#      published.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
worker="$here/codex-exec-worker"
activity="$here/codex-agent-activity"
tmp="$(mktemp -d)"

fail=0
worker_pid=""
cleanup() {
  if [ -n "$worker_pid" ]; then
    kill "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  # The worker forks short-lived children (agent_activity, tick_session)
  # synchronously; killing only the worker's own pid does not reach one
  # already forked when the signal lands, so it can still be writing into
  # $tmp for a moment after `wait` above reaps the worker itself. One retry
  # after a brief pause absorbs that window instead of racing rm -rf's own
  # unlink pass against it.
  rm -rf "$tmp" 2>/dev/null || { sleep 0.3; rm -rf "$tmp"; }
}
trap cleanup EXIT

bin_dir="$tmp/bin"
mkdir -p "$bin_dir"

# The stub copies whatever currently sits at $STUB_REPLY_FILE to
# --output-last-message, re-reading that path fresh on every invocation —
# not its own inherited env at fork time — since the worker (and this stub
# under it) is a long-lived background process started once, before this
# test ever queues a turn; a value merely exported later in this script
# would never reach a process forked before that export ran. Reports a
# thread_id on a fresh call only, real enough to exercise the worker's own
# thread_id bookkeeping without needing a real codex binary.
cat > "$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_ARGV"
printf '%s\0' "$@" > "$STUB_ARGV_NUL"
out=""
resume=false
for i in "$@"; do
  case "$prev" in
    --output-last-message) out="$i" ;;
  esac
  [ "$i" = "resume" ] && resume=true
  prev="$i"
done
if [ "$resume" != true ]; then
  echo '{"type":"thread.started","thread_id":"stub-thread-1"}'
fi
[ -n "$out" ] && cp "$STUB_REPLY_FILE" "$out"
[ -f "$STUB_PAUSE_TURN" ] && { touch "$STUB_IN_TURN"; while [ -f "$STUB_PAUSE_TURN" ]; do sleep 0.1; done; }
exit 0
EOF
chmod +x "$bin_dir/codex"

cat > "$bin_dir/plect" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PLECT_CALLS"
[ "$1 $2" = "state set-message" ] && [ "$4" = "working" ] && [ -f "$PLECT_FAIL_WORKING_ONCE" ] && { rm -f "$PLECT_FAIL_WORKING_ONCE"; exit 1; }
[ "$1 $2" = "state set-message" ] && [ "$4" = "working" ] && [ -f "$PLECT_FAIL_WORKING" ] && exit 1
true
EOF
chmod +x "$bin_dir/plect"

export PATH="$bin_dir:$PATH"
export PLECT_CALLS="$tmp/calls"
export PLECT_SESSION_NAME="selftest/worker-1"
export PLECT_AGENT_ACTIVITY_BIN="$activity"
export CODEX_PUBLISH_MESSAGE=true
export XDG_STATE_HOME="$tmp/xdg-state"
# The path itself, not its content, is what the worker's environment needs
# to carry — fixed and exported before the worker forks, then each turn
# below just overwrites the file at that path.
export STUB_REPLY_FILE="$tmp/next-reply.txt"
export STUB_ARGV="$tmp/argv"
# The last invocation's argv, one NUL-terminated element each, so an
# assertion can see argument boundaries that "$*" above flattens.
export STUB_ARGV_NUL="$tmp/argv-nul"
export PLECT_FAIL_WORKING="$tmp/fail-working"
export PLECT_FAIL_WORKING_ONCE="$tmp/fail-working-once"
export STUB_PAUSE_TURN="$tmp/pause-turn"
export STUB_IN_TURN="$tmp/in-turn"
: > "$STUB_ARGV"
: > "$PLECT_CALLS"
"$activity" reset "$PLECT_SESSION_NAME"

queue_dir="$tmp/queue"
state_dir="$tmp/state"
mkdir -p "$queue_dir"

"$worker" "$queue_dir" "$state_dir" &
worker_pid=$!

wait_for_drain() {
  local file="$1"
  for _ in $(seq 1 200); do
    [ -e "$file" ] || return 0
    sleep 0.1
  done
  return 1
}

# extract_publish isolates the "event publish ..." call from a turn's other
# calls (working/waiting activity, tick) in $PLECT_CALLS: the publish call's
# own body can embed newlines, so it is the one call here that cannot be
# read one physical line at a time.
extract_publish() {
  awk '
    /^event publish/ { capturing=1 }
    capturing && !/^event publish/ && (/^state set-message/ || /^tick/) { exit }
    capturing { print }
  ' "$PLECT_CALLS"
}

# Turn 1: a reply ending in two trailing newlines must survive the worker's
# own file capture (mktemp -> codex -> jq --rawfile) byte for byte, not just
# whatever a $(cat ...) command substitution would have left after stripping
# them.
: > "$PLECT_CALLS"
printf 'paragraph one\n\n' > "$STUB_REPLY_FILE"
echo '{"type":"user_message","text":"turn one"}' > "$queue_dir/t1.json"
wait_for_drain "$queue_dir/t1.json" || { echo "FAIL turn 1 was never drained from the queue" >&2; fail=1; }

got="$(extract_publish)"
want='event publish selftest/worker-1 --type plect.message --summary paragraph one --body paragraph one

 --meta message_id=selftest/worker-1/reply-0 --meta message_id_origin=synthetic --meta role=assistant --meta source=codex'
if [ "$got" = "$want" ]; then
  echo "ok   a trailing-newline reply survives the worker's own capture byte for byte"
else
  printf 'FAIL trailing-newline capture: got %q, want %q\n' "$got" "$want" >&2
  fail=1
fi

# Simulate exec_runtime's setup calling reset on a restart — before the
# session's second turn, the way a real relaunch (resume included) would.
"$activity" reset "$PLECT_SESSION_NAME"

# Turn 2 (resume, since the stub already reported a thread_id): message_id
# must advance to reply-1, not reuse reply-0 — reset must not have rewound
# the counter this runtime's every message_id depends on.
: > "$PLECT_CALLS"
printf 'turn two reply' > "$STUB_REPLY_FILE"
echo '{"type":"user_message","text":"turn two"}' > "$queue_dir/t2.json"
wait_for_drain "$queue_dir/t2.json" || { echo "FAIL turn 2 was never drained from the queue" >&2; fail=1; }

got="$(extract_publish)"
case "$got" in
  *"message_id=selftest/worker-1/reply-1"*)
    echo "ok   message_id keeps advancing across a reset between turns" ;;
  *)
    printf 'FAIL a reset between turns must not reuse a message_id, got: %s\n' "$got" >&2
    fail=1 ;;
esac

kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

: > "$STUB_ARGV"
priority_queue="$tmp/priority-queue"
priority_state="$tmp/priority-state"
mkdir -p "$priority_queue"
echo '{"type":"user.emit","text":"leftover turn"}' > "$priority_queue/1-leftover.json"
"$worker" "$priority_queue" "$priority_state" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$priority_state/ready" ] && break
  sleep 0.01
done
[ -e "$priority_state/ready" ] || { echo "FAIL resumed worker was never ready" >&2; fail=1; }
echo '{"type":"plect.session.resumed","text":"resume instruction"}' > "$priority_queue/2-resumed.json"
echo '{"type":"user.emit","text":"later turn"}' > "$priority_queue/3-later.json"
wait_for_drain "$priority_queue/3-later.json" || { echo "FAIL queued turns were never drained" >&2; fail=1; }
mapfile -t ordered_turns < "$STUB_ARGV"
if [ "${#ordered_turns[@]}" -eq 3 ] &&
   [[ "${ordered_turns[0]}" == *"-- resume instruction"* ]] &&
   [[ "${ordered_turns[1]}" == *"-- leftover turn"* ]] &&
   [[ "${ordered_turns[2]}" == *"-- later turn"* ]]; then
  echo "ok   a resume instruction precedes an interrupted turn; other turns keep queue order"
else
  printf 'FAIL queued turn order: %s\n' "${ordered_turns[*]}" >&2
  fail=1
fi
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

timeout_queue="$tmp/timeout-queue"
timeout_state="$tmp/timeout-state"
mkdir -p "$timeout_queue"
echo '{"type":"user.emit","text":"leftover without notice"}' > "$timeout_queue/1-leftover.json"
: > "$STUB_ARGV"
"$worker" "$timeout_queue" "$timeout_state" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$timeout_state/ready" ] && break
  sleep 0.01
done
[ -e "$timeout_state/ready" ] || { echo "FAIL fallback worker was never ready" >&2; fail=1; }
sleep 0.5
if [ -s "$STUB_ARGV" ]; then
  echo "FAIL leftover turn ran before the resume-instruction wait expired" >&2
  fail=1
fi
wait_for_drain "$timeout_queue/1-leftover.json" || { echo "FAIL leftover turn was never drained without a notice" >&2; fail=1; }
if [ "$(wc -l < "$STUB_ARGV")" -eq 1 ] && grep -q -- '-- leftover without notice' "$STUB_ARGV"; then
  echo "ok   a queued turn runs once after the resume-instruction wait expires"
else
  echo "FAIL queued turn did not run once after the wait expired" >&2
  fail=1
fi
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

blocked_queue="$tmp/blocked-queue"
blocked_state="$tmp/blocked-state"
mkdir -p "$blocked_queue"
echo '{"type":"user.emit","text":"blocked turn"}' > "$blocked_queue/1.json"
: > "$STUB_ARGV"
touch "$PLECT_FAIL_WORKING"
"$worker" "$blocked_queue" "$blocked_state" > "$tmp/blocked.out" 2>&1 &
worker_pid=$!
sleep 7
if [ -s "$STUB_ARGV" ] || [ ! -e "$blocked_queue/1.json" ] || ! grep -q 'working' "$tmp/blocked.out"; then
  echo "FAIL a failed working status write started or lost a turn without reporting failure" >&2
  fail=1
else
  echo "ok   a failed working status write leaves the turn queued and reports failure"
fi
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
rm -f "$PLECT_FAIL_WORKING"

retry_queue="$tmp/retry-queue"
retry_state="$tmp/retry-state"
mkdir -p "$retry_queue"
echo '{"type":"user.emit","text":"retry turn"}' > "$retry_queue/1.json"
touch "$PLECT_FAIL_WORKING_ONCE" "$STUB_PAUSE_TURN"
"$worker" "$retry_queue" "$retry_state" > "$tmp/retry.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$STUB_IN_TURN" ] && break
  sleep 0.1
done
if [ ! -e "$STUB_IN_TURN" ] || ! jq -e '.last_exit_code == 0' "$retry_state/health.json" >/dev/null 2>&1; then
  echo "FAIL health probe is not passing during a turn after a retried working write" >&2
  fail=1
else
  echo "ok   health probe passes during a turn after a retried working write"
fi
rm -f "$STUB_PAUSE_TURN"
wait_for_drain "$retry_queue/1.json" || { echo "FAIL retried turn was never drained" >&2; fail=1; }
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

# Given mcp_servers overrides in the state directory, both a first turn and a
# resumed turn carry each one as a single `-c` argument, quotes and spaces
# intact; given none, a turn's argv carries no `-c` at all.
last_argv() {
  local -n out="$1"
  mapfile -d '' out < "$STUB_ARGV_NUL"
}
has_pair() {
  local flag="$1" value="$2"; shift 2
  local args=("$@") i
  for ((i = 0; i + 1 < ${#args[@]}; i++)); do
    [ "${args[i]}" = "$flag" ] && [ "${args[i+1]}" = "$value" ] && return 0
  done
  return 1
}

mcp_queue="$tmp/mcp-queue"
mcp_state="$tmp/mcp-state"
mkdir -p "$mcp_queue" "$mcp_state"
override='mcp_servers.x={command = "stub mcp", args = ["a b","c\"d","it'"'"'s"], env = {"K 1" = "v \"q\""}}'
jq -n --arg o "$override" --arg p 'mcp_servers.y={command = "two"}' '[$o, $p]' > "$mcp_state/mcp_overrides.json"
"$worker" "$mcp_queue" "$mcp_state" &
worker_pid=$!
for turn in first resumed; do
  : > "$STUB_ARGV_NUL"
  echo "{\"type\":\"user.emit\",\"text\":\"$turn turn\"}" > "$mcp_queue/$turn.json"
  wait_for_drain "$mcp_queue/$turn.json" || { echo "FAIL $turn mcp turn was never drained" >&2; fail=1; }
  last_argv argv
  if [ "$turn" = resumed ] && [ "${argv[1]-}" != resume ]; then
    printf 'FAIL the second mcp turn was not a resume: %s\n' "${argv[*]}" >&2
    fail=1
  elif has_pair -c "$override" "${argv[@]}" && has_pair -c 'mcp_servers.y={command = "two"}' "${argv[@]}"; then
    echo "ok   a $turn turn carries each mcp_servers override as one intact argument"
  else
    printf 'FAIL a %s turn lacks an intact mcp_servers override: %s\n' "$turn" "$(printf '%q ' "${argv[@]}")" >&2
    fail=1
  fi
done
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

plain_queue="$tmp/plain-queue"
plain_state="$tmp/plain-state"
mkdir -p "$plain_queue"
: > "$STUB_ARGV_NUL"
"$worker" "$plain_queue" "$plain_state" &
worker_pid=$!
echo '{"type":"user.emit","text":"plain turn"}' > "$plain_queue/1.json"
wait_for_drain "$plain_queue/1.json" || { echo "FAIL plain turn was never drained" >&2; fail=1; }
last_argv argv
if [ "${#argv[@]}" -eq 7 ] &&
   [ "${argv[*]:0:4}" = "exec --json --dangerously-bypass-approvals-and-sandbox --output-last-message" ] &&
   [ "${argv[*]:5}" = "-- plain turn" ]; then
  echo "ok   without mcp_servers a turn's argv is unchanged"
else
  printf 'FAIL without mcp_servers the argv changed: %s\n' "$(printf '%q ' "${argv[@]}")" >&2
  fail=1
fi
kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

[ "$fail" -eq 0 ] || { echo "codex-exec-worker selftest failed" >&2; exit 1; }
echo "codex-exec-worker selftest passed"
