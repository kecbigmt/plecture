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
printf '%s' "${@: -1}" > "$STUB_PROMPT_FILE"
[ -n "${STUB_HEALTH_FILE:-}" ] && jq -r '.last_exit_code // "missing"' "$STUB_HEALTH_FILE" >> "$STUB_HEALTH_LOG" 2>/dev/null
mode="$(cat "$STUB_MODE_FILE" 2>/dev/null || true)"
[ "$mode" = fail ] && exit 1
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
[ -f "$STUB_PAUSE_TURN" ] && { printf '%s' "$$" > "$STUB_CHILD_PID"; touch "$STUB_IN_TURN"; while [ -f "$STUB_PAUSE_TURN" ]; do sleep 0.1; done; }
exit 0
EOF
chmod +x "$bin_dir/codex"

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
export PLECT_FAIL_WORKING="$tmp/fail-working"
export PLECT_FAIL_WORKING_ONCE="$tmp/fail-working-once"
export PLECT_FAIL_PUBLISH="$tmp/fail-publish"
export STUB_PAUSE_TURN="$tmp/pause-turn"
export STUB_IN_TURN="$tmp/in-turn"
export STUB_CHILD_PID="$tmp/child-pid"
export STUB_PROMPT_FILE="$tmp/prompt"
export STUB_MODE_FILE="$tmp/mode"
export STUB_HEALTH_FILE=""
export STUB_HEALTH_LOG="$tmp/health-observed"
printf 'ok' > "$STUB_MODE_FILE"
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
    [ ! -e "$file" ] && [ ! -d "$(dirname "$file")/.inflight" ] && return 0
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
if [[ "$got" == $'event publish selftest/worker-1 --type plect.message --summary paragraph one --body paragraph one\n\n --meta message_id=selftest/worker-1/'* ]] &&
   [[ "$got" == *'--meta message_id_origin=synthetic --meta role=assistant --meta source=codex --meta turn_id='* ]]; then
  echo "ok   a trailing-newline reply survives the worker's own capture byte for byte"
else
  printf 'FAIL trailing-newline capture: got %q\n' "$got" >&2
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
if [[ "$got" == *"message_id=selftest/worker-1/"* ]] && [[ "$got" != *"message_id=selftest/worker-1/reply-0"* ]]; then
  echo "ok   the next batch gets a distinct message id after reset"
else
  printf 'FAIL a reset reused a message id: %s\n' "$got" >&2
  fail=1
fi

jq -n --arg text $'end\n\n' '{type:"user.emit", text:$text}' > "$queue_dir/t-nl.json"
wait_for_drain "$queue_dir/t-nl.json" || { echo "FAIL trailing-newline input was not delivered" >&2; fail=1; }
if tail -c 7 "$STUB_PROMPT_FILE" | cmp -s - <(printf 'end\n\n\n\n'); then
  echo "ok   the last message's trailing newlines reach the agent"
else
  echo "FAIL the last message's trailing newlines were stripped" >&2
  fail=1
fi

kill "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

crash_queue="$tmp/crash-queue"
crash_state="$tmp/crash-state"
mkdir -p "$crash_queue"
echo '{"id":"one","type":"example","text":"crash first"}' > "$crash_queue/1.json"
echo '{"id":"two","type":"example","text":"crash second"}' > "$crash_queue/2.json"
: > "$STUB_ARGV"
rm -f "$STUB_IN_TURN" "$STUB_CHILD_PID"
touch "$STUB_PAUSE_TURN"
"$worker" "$crash_queue" "$crash_state" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -s "$STUB_CHILD_PID" ] && break
  sleep 0.1
done
child_pid="$(cat "$STUB_CHILD_PID" 2>/dev/null || true)"
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
child_gone=false
for _ in $(seq 1 50); do
  if [ -n "$child_pid" ] && ! kill -0 "$child_pid" 2>/dev/null; then
    child_gone=true
    break
  fi
  sleep 0.1
done
if [ "$child_gone" != true ]; then
  echo "FAIL a killed worker left its agent invocation running" >&2
  fail=1
  kill -TERM "$child_pid" 2>/dev/null || true
fi
rm -f "$STUB_PAUSE_TURN"
"$worker" "$crash_queue" "$crash_state" &
worker_pid=$!
wait_for_drain "$crash_queue/1.json" || { echo "FAIL interrupted batch was not retried" >&2; fail=1; }
if [ "$(grep -c 'crash first' "$STUB_ARGV" || true)" -eq 2 ] &&
   [ "$(grep -c 'crash second' "$STUB_ARGV" || true)" -eq 2 ]; then
  echo "ok   an interrupted batch retries all entries together"
else
  echo "FAIL interrupted batch did not retry both entries" >&2
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
"$worker" "$priority_queue" "$priority_state" &
worker_pid=$!
for _ in $(seq 1 100); do
  [ -e "$priority_state/ready" ] && break
  sleep 0.01
done
[ -e "$priority_state/ready" ] || { echo "FAIL resumed worker was never ready" >&2; fail=1; }
wait_for_drain "$priority_queue/3-later.json" || { echo "FAIL queued turns were never drained" >&2; fail=1; }
batch_args="$(cat "$STUB_ARGV")"
if [ "$(grep -c -- '-- Consider and answer every numbered entry' "$STUB_ARGV" || true)" -eq 1 ] &&
   [[ "$batch_args" == *"leftover turn"*"resume instruction"*"later turn"* ]]; then
  echo "ok   pending entries form one turn in queue order"
else
  printf 'FAIL queued batch: %s\n' "$(cat "$STUB_ARGV")" >&2
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
wait_for_drain "$timeout_queue/1-leftover.json" || { echo "FAIL leftover turn was never drained without a notice" >&2; fail=1; }
if [ "$(grep -c -- '-- Consider and answer every numbered entry' "$STUB_ARGV" || true)" -eq 1 ] && grep -q 'leftover without notice' "$STUB_ARGV"; then
  echo "ok   a queued turn runs once without a resume instruction"
else
  echo "FAIL queued turn did not run once" >&2
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
if [ -s "$STUB_ARGV" ] || [ ! -e "$blocked_queue/.inflight/1.json" ] || ! grep -q 'working' "$tmp/blocked.out"; then
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
rm -f "$STUB_IN_TURN" "$STUB_CHILD_PID"
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

publish_queue="$tmp/publish-queue"
publish_state="$tmp/publish-state"
mkdir -p "$publish_queue"
echo '{"id":"publish","type":"example","text":"publish failure"}' > "$publish_queue/1.json"
: > "$PLECT_CALLS"
: > "$STUB_ARGV"
touch "$PLECT_FAIL_PUBLISH"
"$worker" "$publish_queue" "$publish_state" &
worker_pid=$!
for _ in $(seq 1 100); do
  grep -q '^event publish' "$PLECT_CALLS" && jq -e '.last_exit_code == 1' "$publish_state/health.json" >/dev/null 2>&1 && break
  sleep 0.1
done
if [ ! -f "$publish_queue/.inflight/1.json" ]; then
  echo "FAIL a reply publication failure acknowledged the batch" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
rm -f "$PLECT_FAIL_PUBLISH"
"$worker" "$publish_queue" "$publish_state" &
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
printf 'fail' > "$STUB_MODE_FILE"
"$worker" "$bounded_queue" "$bounded_state" > "$tmp/bounded.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 150); do
  compgen -G "$bounded_queue/.failed-*" >/dev/null && break
  sleep 0.1
done
if ! compgen -G "$bounded_queue/.failed-*" >/dev/null || [ "$(grep -c 'poison turn' "$STUB_ARGV" || true)" -ne 3 ]; then
  echo "FAIL a deterministic agent failure was not parked after three attempts" >&2
  fail=1
fi
if [ "$(sed -n '2,3p' "$STUB_HEALTH_LOG" | grep -c '^0$' || true)" -ne 2 ]; then
  echo "FAIL health did not recover before each agent retry: $(cat "$STUB_HEALTH_LOG")" >&2
  fail=1
fi
printf 'ok' > "$STUB_MODE_FILE"
echo '{"id":"later","type":"example","text":"later turn"}' > "$bounded_queue/2.json"
wait_for_drain "$bounded_queue/2.json" || { echo "FAIL a parked batch blocked newer work" >&2; fail=1; }
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
export STUB_HEALTH_FILE=""

reply_queue="$tmp/reply-queue"
reply_state="$tmp/reply-state"
mkdir -p "$reply_queue"
echo '{"id":"reply","type":"example","text":"stuck publication"}' > "$reply_queue/1.json"
: > "$STUB_ARGV"
touch "$PLECT_FAIL_PUBLISH"
"$worker" "$reply_queue" "$reply_state" > "$tmp/reply.out" 2>&1 &
worker_pid=$!
for _ in $(seq 1 150); do
  compgen -G "$reply_queue/.failed-*" >/dev/null && break
  sleep 0.1
done
if ! compgen -G "$reply_queue/.failed-*" >/dev/null || [ "$(grep -c 'stuck publication' "$STUB_ARGV" || true)" -ne 1 ]; then
  echo "FAIL repeated publication failures reran the agent or never parked" >&2
  fail=1
fi
if ! compgen -G "$reply_queue/.failed-*/reply.txt" >/dev/null; then
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
"$worker" "$ack_queue" "$ack_state" > "$tmp/ack.out" 2>&1 &
worker_pid=$!
wait_for_drain "$ack_queue/1.json" || { echo "FAIL completed batch was not acknowledged: $(cat "$tmp/ack.out")" >&2; find "$ack_queue" -maxdepth 2 -type f >&2; fail=1; }
if [ -s "$STUB_ARGV" ]; then
  echo "FAIL already published batch invoked the agent again" >&2
  fail=1
fi
kill -TERM "$worker_pid" 2>/dev/null || true
wait "$worker_pid" 2>/dev/null || true
worker_pid=""

bash "$here/../../headless-queue-conformance.sh" "$here/headless-batch.sh" || fail=1
[ "$fail" -eq 0 ] || { echo "codex-exec-worker selftest failed" >&2; exit 1; }
echo "codex-exec-worker selftest passed"
