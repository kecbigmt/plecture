#!/usr/bin/env bash
# Proves the worker's contract with `codex exec` and with the session inbox
# across its real process boundaries — a stubbed `codex`, a stubbed `plect`
# standing in for the inbox, and a background worker process — because every
# property here lives at one of those boundaries:
#
#   1. A turn's captured reply reaches plect.message byte for byte, trailing
#      newlines included, under a message id that carries the batch id.
#   2. One batch becomes one prompt: every event numbered in order, shaped by
#      message_envelope, byte for byte.
#   3. A batch is acknowledged only after its turn ran and its reply was
#      published; a failure at either step leaves it for redelivery, and a
#      finished turn is never run twice for one batch.
#   4. A failed working-status write holds the batch without taking again.
#   5. mcp_servers overrides reach every turn as intact arguments.
#
# Which events a batch holds, redelivery and parking are core's and are
# specified by its own tests; the stub only replays that contract.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
worker="$here/codex-exec-worker"
activity="$here/codex-agent-activity"
tmp="$(mktemp -d)"

fail=0
worker_pid=""
stop_worker() {
  if [ -n "$worker_pid" ]; then
    kill "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
    worker_pid=""
  fi
}
# The worker forks short-lived children (agent_activity, tick_session)
# synchronously; one already forked when the signal lands can still be
# writing into $tmp for a moment, so the removal is retried once.
cleanup() {
  stop_worker
  rm -rf "$tmp" 2>/dev/null || { sleep 0.3; rm -rf "$tmp"; }
}
trap cleanup EXIT

bin_dir="$tmp/bin"
mkdir -p "$bin_dir"

# The stub copies whatever currently sits at $STUB_REPLY_FILE to
# --output-last-message, re-reading that path fresh on every invocation,
# since the worker (and this stub under it) is forked before a test queues a
# turn. It reports a thread_id on a fresh call only, which is enough to
# exercise the worker's own thread_id bookkeeping.
cat > "$bin_dir/codex" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_ARGV"
printf '%s' "${@: -1}" > "$STUB_PROMPT_FILE"
[ -n "${STUB_HEALTH_FILE:-}" ] && jq -r '.last_exit_code // "missing"' "$STUB_HEALTH_FILE" >> "$STUB_HEALTH_LOG" 2>/dev/null
mode="$(cat "$STUB_MODE_FILE" 2>/dev/null || true)"
[ "$mode" = fail ] && exit 1
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
[ -f "$STUB_PAUSE_TURN" ] && { printf '%s' "$$" > "$STUB_CHILD_PID"; touch "$STUB_IN_TURN"; while [ -f "$STUB_PAUSE_TURN" ]; do sleep 0.1; done; }
exit 0
EOF
chmod +x "$bin_dir/codex"

# The inbox half of the stub keeps one outstanding batch per $FAKE_INBOX the
# way core does: a take returns it again and counts a delivery until an ack
# names it, and a fourth take parks it.
cat > "$bin_dir/plect" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PLECT_CALLS"
box="${FAKE_INBOX:?}"
case "$1 $2" in
  "inbox take")
    if [ -f "$box/batch.json" ] && [ "$(jq '.deliveries' "$box/batch.json")" -ge 3 ]; then
      jq -r '.batch' "$box/batch.json" >> "$box/parked"
      rm -f "$box/batch.json"
    fi
    if [ ! -f "$box/batch.json" ]; then
      if ! ls "$box/pending"/*.json >/dev/null 2>&1; then
        sleep 0.2
        exit 0
      fi
      n=$(($(cat "$box/seq" 2>/dev/null || echo 0) + 1))
      echo "$n" > "$box/seq"
      jq -sc --arg id "batch-$n" '{batch: $id, deliveries: 0, events: .}' "$box/pending"/*.json > "$box/batch.tmp"
      rm -f "$box/pending"/*.json
      mv "$box/batch.tmp" "$box/batch.json"
    fi
    jq -c '.deliveries += 1' "$box/batch.json" > "$box/batch.tmp" && mv "$box/batch.tmp" "$box/batch.json"
    cat "$box/batch.json"
    ;;
  "inbox ack")
    [ -f "$PLECT_FAIL_ACK" ] && exit 1
    [ "$(jq -r '.batch' "$box/batch.json" 2>/dev/null)" = "$4" ] || exit 1
    rm -f "$box/batch.json"
    echo "$4" >> "$box/acked"
    ;;
  "state set-message")
    if [ "$4" = "working" ]; then
      [ -f "$PLECT_FAIL_WORKING_ONCE" ] && { rm -f "$PLECT_FAIL_WORKING_ONCE"; exit 1; }
      [ -f "$PLECT_FAIL_WORKING" ] && exit 1
    fi
    ;;
  "event publish") [ -f "$PLECT_FAIL_PUBLISH" ] && exit 1 ;;
esac
true
EOF
chmod +x "$bin_dir/plect"

export PATH="$bin_dir:$PATH"
export PLECT_CALLS="$tmp/calls"
export PLECT_SESSION_NAME="selftest/worker-1"
export PLECT_AGENT_ACTIVITY_BIN="$activity"
export CODEX_PUBLISH_MESSAGE=true
# A Codex session running this test may export these; the argv assertions
# below expect the worker's flag-free baseline.
unset CODEX_MODEL CODEX_MODEL_REASONING_EFFORT
export XDG_STATE_HOME="$tmp/xdg-state"
export STUB_REPLY_FILE="$tmp/next-reply.txt"
export STUB_ARGV="$tmp/argv"
# The last invocation's argv, one NUL-terminated element each, so an
# assertion can see argument boundaries that "$*" flattens.
export STUB_ARGV_NUL="$tmp/argv-nul"
export PLECT_FAIL_WORKING="$tmp/fail-working"
export PLECT_FAIL_WORKING_ONCE="$tmp/fail-working-once"
export PLECT_FAIL_PUBLISH="$tmp/fail-publish"
export PLECT_FAIL_ACK="$tmp/fail-ack"
export STUB_PAUSE_TURN="$tmp/pause-turn"
export STUB_IN_TURN="$tmp/in-turn"
export STUB_CHILD_PID="$tmp/child-pid"
export STUB_PROMPT_FILE="$tmp/prompt"
export STUB_MODE_FILE="$tmp/mode"
export STUB_HEALTH_FILE=""
export STUB_HEALTH_LOG="$tmp/health-observed"
printf 'ok' > "$STUB_MODE_FILE"
printf 'done' > "$STUB_REPLY_FILE"
: > "$STUB_ARGV"
: > "$PLECT_CALLS"
"$activity" reset "$PLECT_SESSION_NAME"

box=""
state_dir=""

# scenario gives each property its own inbox and state directory, so one
# property's leftovers cannot satisfy another's assertion.
scenario() {
  stop_worker
  box="$tmp/scenario/$1/inbox"
  state_dir="$tmp/scenario/$1/state"
  mkdir -p "$box/pending" "$state_dir"
  : > "$STUB_ARGV"
  : > "$PLECT_CALLS"
  printf 'ok' > "$STUB_MODE_FILE"
  printf 'done' > "$STUB_REPLY_FILE"
  rm -f "$PLECT_FAIL_WORKING" "$PLECT_FAIL_WORKING_ONCE" "$PLECT_FAIL_PUBLISH" "$PLECT_FAIL_ACK" "$STUB_PAUSE_TURN" "$STUB_IN_TURN"
}

start_worker() {
  FAKE_INBOX="$box" "$worker" "$state_dir" 2>> "$tmp/worker.err" &
  worker_pid=$!
}

seq_no=0
hand_over() {
  seq_no=$((seq_no + 1))
  jq -n --arg id "$1" --arg type "$2" --arg body "$3" --arg url "${4-}" \
    '{id: $id, type: $type, source: "selftest", summary: "summary of \($id)", body: $body}
     + (if $url == "" then {} else {metadata: {url: $url}} end)' \
    > "$box/pending/$(printf '%04d' "$seq_no").json"
}

wait_until() {
  local _
  for _ in $(seq 1 "${2:-150}"); do
    eval "$1" && return 0
    sleep 0.1
  done
  return 1
}

acked() { [ -f "$box/acked" ] && [ "$(wc -l < "$box/acked")" -ge "$1" ]; }
# A prompt spans several lines of the stub's argv log, so invocations are
# counted by the line each one starts with.
invocations() { grep -c '^exec ' "$STUB_ARGV" || true; }
calls() { grep -c -- "$1" "$PLECT_CALLS" || true; }

check() {
  if eval "$2"; then
    echo "ok   $1"
  else
    echo "FAIL $1" >&2
    fail=1
  fi
}

# extract_publish isolates the "event publish ..." call from a turn's other
# calls in $PLECT_CALLS: the publish call's own body can embed newlines, so
# it is the one call here that cannot be read one physical line at a time.
extract_publish() {
  awk '
    /^event publish/ { capturing=1 }
    capturing && !/^event publish/ && (/^state set-message/ || /^tick/ || /^inbox/) { exit }
    capturing { print }
  ' "$PLECT_CALLS"
}

# --- reply capture -------------------------------------------------------------
scenario reply
printf 'paragraph one\n\n' > "$STUB_REPLY_FILE"
start_worker
hand_over t1 user.emit "turn one"
wait_until 'acked 1' || { echo "FAIL turn one was never acknowledged" >&2; fail=1; }
got="$(extract_publish)"
if [[ "$got" == $'event publish selftest/worker-1 --type plect.message --summary paragraph one --body paragraph one\n\n --meta message_id=selftest/worker-1/batch-1 --meta message_id_origin=synthetic --meta role=assistant --meta source=codex --meta turn_id=batch-1'* ]]; then
  echo "ok   a trailing-newline reply reaches plect.message byte for byte under the batch's id"
else
  printf 'FAIL reply capture: got %q\n' "$got" >&2
  fail=1
fi
check "the first turn starts a thread and records its id" '[ "$(cat "$state_dir/thread_id")" = "stub-thread-1" ] && ! grep -q "exec resume" "$STUB_ARGV"'
"$activity" reset "$PLECT_SESSION_NAME"
: > "$PLECT_CALLS"
printf 'turn two reply' > "$STUB_REPLY_FILE"
hand_over t2 user.emit "turn two"
wait_until 'acked 2' || { echo "FAIL turn two was never acknowledged" >&2; fail=1; }
check "a later batch resumes the thread under its own message id" 'grep -q "^exec resume" "$STUB_ARGV" && extract_publish | grep -q -- "--meta message_id=selftest/worker-1/batch-2"'
check "a turn ticks after its acknowledgement" '[ "$(calls "^tick selftest/worker-1")" -eq 1 ]'
check "the kept reply is dropped after the acknowledgement" '! ls "$state_dir"/reply-* >/dev/null 2>&1'

# --- one batch, one prompt -----------------------------------------------------
scenario prompt
hand_over a user.emit "first body"
hand_over b github.pull_request "" "https://example.test/pr/1"
hand_over c user.emit $'end\n\n'
start_worker
wait_until 'acked 1' || { echo "FAIL a three-event batch was never acknowledged" >&2; fail=1; }
check "pending events form one turn" '[ "$(invocations)" -eq 1 ]'
want="$(printf 'Consider and answer every numbered entry in this batch in one turn. Reply once after considering all entries.\n\n[1] id=a type=user.emit source=selftest\n[user.emit] first body\n\n[2] id=b type=github.pull_request source=selftest\n[github.pull_request] summary of b (https://example.test/pr/1)\n\n[3] id=c type=user.emit source=selftest\n[user.emit] end\n\n\n\n'; printf x)"
if [ "$(cat "$STUB_PROMPT_FILE"; printf x)" = "$want" ]; then
  echo "ok   the prompt numbers every event in order and keeps trailing newlines"
else
  printf 'FAIL prompt: got %q\n' "$(cat "$STUB_PROMPT_FILE")" >&2
  fail=1
fi

scenario envelope
printf '%s' '<{type}|{body}|{summary}|{url}>' > "$state_dir/message_envelope"
hand_over e user.emit "the body" "https://example.test/x"
start_worker
wait_until 'acked 1' || { echo "FAIL an enveloped batch was never acknowledged" >&2; fail=1; }
check "message_envelope shapes each event" 'grep -qF "<user.emit|the body|summary of e|https://example.test/x>" "$STUB_PROMPT_FILE"'

# --- termination ---------------------------------------------------------------
scenario terminate
touch "$STUB_PAUSE_TURN"
start_worker
hand_over t4 user.emit "turn four"
wait_until '[ -e "$STUB_IN_TURN" ]' || { echo "FAIL the paused turn never started" >&2; fail=1; }
child_pid="$(cat "$STUB_CHILD_PID" 2>/dev/null || true)"
kill -TERM "$worker_pid"
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
if [ -n "$child_pid" ] && wait_until '! kill -0 "$child_pid" 2>/dev/null' 30; then
  echo "ok   terminating the worker mid-turn terminates the running codex"
else
  echo "FAIL codex (pid ${child_pid:-unknown}) outlived its terminated worker" >&2
  fail=1
fi
rm -f "$STUB_PAUSE_TURN"
check "an interrupted batch stays unacknowledged" '[ ! -e "$box/acked" ] && [ -f "$box/batch.json" ]'
start_worker
wait_until 'acked 1' || { echo "FAIL an interrupted batch was not delivered again" >&2; fail=1; }
check "a restarted worker runs the interrupted batch again and acknowledges it once" '[ "$(grep -c "turn four" "$STUB_ARGV")" -eq 2 ] && [ "$(cat "$box/acked")" = "batch-1" ]'

scenario idle
start_worker
wait_until '[ -e "$state_dir/ready" ] && [ "$(calls "^inbox take")" -ge 1 ]' || { echo "FAIL the idle worker never took" >&2; fail=1; }
idle_pid="$worker_pid"
kill -TERM "$idle_pid"
if wait_until '! kill -0 "$idle_pid" 2>/dev/null' 30; then
  echo "ok   terminating an idle worker stops it while it waits on the inbox"
else
  echo "FAIL an idle worker ignored TERM" >&2
  fail=1
fi
wait "$idle_pid" 2>/dev/null || true
worker_pid=""

# --- working status --------------------------------------------------------------
scenario status
touch "$PLECT_FAIL_WORKING"
start_worker
hand_over s1 user.emit "status turn"
wait_until 'jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null 2>&1' || true
sleep 1.5
check "a failed working status write starts no turn and reports failure" '[ "$(invocations)" -eq 0 ] && jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null'
check "a failed working status write does not take the batch again" '[ "$(jq ".deliveries" "$box/batch.json")" -eq 1 ]'
rm -f "$PLECT_FAIL_WORKING"
wait_until 'acked 1' || { echo "FAIL the held batch never ran" >&2; fail=1; }
check "the held batch runs once the status write succeeds" '[ "$(invocations)" -eq 1 ]'

scenario status-once
export STUB_HEALTH_FILE="$state_dir/health.json"
: > "$STUB_HEALTH_LOG"
touch "$PLECT_FAIL_WORKING_ONCE"
start_worker
hand_over s2 user.emit "retried status"
wait_until 'acked 1' || { echo "FAIL a batch behind a retried status write never ran" >&2; fail=1; }
check "health passes during a turn that follows a retried working write" '[ "$(cat "$STUB_HEALTH_LOG")" = "0" ]'

# --- agent failure ---------------------------------------------------------------
scenario poison
: > "$STUB_HEALTH_LOG"
export STUB_HEALTH_FILE="$state_dir/health.json"
printf 'fail' > "$STUB_MODE_FILE"
start_worker
hand_over p1 user.emit "poison turn"
wait_until '[ -s "$box/parked" ]' 250 || { echo "FAIL a failing batch was never given up" >&2; fail=1; }
check "a failing batch is run once per delivery and no more" '[ "$(grep -c "poison turn" "$STUB_ARGV")" -eq 3 ]'
check "health recovers before every retry of the agent" '[ "$(sort -u "$STUB_HEALTH_LOG")" = "0" ]'
printf 'ok' > "$STUB_MODE_FILE"
hand_over p2 user.emit "after poison"
wait_until 'acked 1' || { echo "FAIL a parked batch blocked newer work" >&2; fail=1; }
check "the worker moves on to the next batch after core parks one" '[ "$(cat "$box/acked")" = "batch-2" ]'
export STUB_HEALTH_FILE=""

# --- reply publication -------------------------------------------------------------
scenario publish
touch "$PLECT_FAIL_PUBLISH"
start_worker
hand_over r1 user.emit "publish turn"
wait_until '[ "$(jq ".deliveries" "$box/batch.json" 2>/dev/null || echo 0)" -ge 2 ]' || { echo "FAIL a batch with a failed publication was not delivered again" >&2; fail=1; }
check "a failed reply publication does not acknowledge the batch" '[ ! -e "$box/acked" ] && jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null'
rm -f "$PLECT_FAIL_PUBLISH"
wait_until 'acked 1' || { echo "FAIL the reply publication was not retried" >&2; fail=1; }
check "a publication retry does not run the agent again" '[ "$(invocations)" -eq 1 ]'

scenario ack
touch "$PLECT_FAIL_ACK"
start_worker
hand_over k1 user.emit "ack turn"
wait_until '[ "$(jq ".deliveries" "$box/batch.json" 2>/dev/null || echo 0)" -ge 2 ]' || { echo "FAIL a batch with a failed acknowledgement was not delivered again" >&2; fail=1; }
rm -f "$PLECT_FAIL_ACK"
wait_until 'acked 1' || { echo "FAIL the acknowledgement was not retried" >&2; fail=1; }
check "an acknowledgement retry neither runs the agent nor publishes again" '[ "$(invocations)" -eq 1 ] && [ "$(calls "^event publish")" -eq 1 ]'

scenario opt-out
export CODEX_PUBLISH_MESSAGE=false
start_worker
hand_over o1 user.emit "quiet turn"
wait_until 'acked 1' || { echo "FAIL an opted-out batch was never acknowledged" >&2; fail=1; }
check "a runtime that publishes no reply still acknowledges its batch" '[ "$(invocations)" -eq 1 ] && [ "$(calls "^event publish")" -eq 0 ]'
export CODEX_PUBLISH_MESSAGE=true

# --- mcp_servers overrides -----------------------------------------------------------
# Given mcp_servers overrides in the state directory, both a first turn and a
# resumed turn carry each one as a single `-c` argument, quotes and spaces
# intact; given none, a turn's argv carries no `-c` at all.
last_argv() {
  mapfile -d '' "$1" < "$STUB_ARGV_NUL"
}
has_pair() {
  local flag="$1" value="$2"; shift 2
  local args=("$@") i
  for ((i = 0; i + 1 < ${#args[@]}; i++)); do
    [ "${args[i]}" = "$flag" ] && [ "${args[i+1]}" = "$value" ] && return 0
  done
  return 1
}

scenario mcp
override='mcp_servers.x={command = "stub mcp", args = ["a b","c\"d","it'"'"'s"], env = {"K 1" = "v \"q\""}}'
jq -n --arg o "$override" --arg p 'mcp_servers.y={command = "two"}' '[$o, $p]' > "$state_dir/mcp_overrides.json"
start_worker
n=0
for turn in first resumed; do
  n=$((n + 1))
  : > "$STUB_ARGV_NUL"
  hand_over "m$n" user.emit "$turn turn"
  wait_until "acked $n" || { echo "FAIL $turn mcp turn was never acknowledged" >&2; fail=1; }
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

scenario plain
: > "$STUB_ARGV_NUL"
start_worker
hand_over plain user.emit "plain turn"
wait_until 'acked 1' || { echo "FAIL plain turn was never acknowledged" >&2; fail=1; }
last_argv argv
if [ "${#argv[@]}" -eq 7 ] &&
   [ "${argv[*]:0:4}" = "exec --json --dangerously-bypass-approvals-and-sandbox --output-last-message" ] &&
   [ "${argv[5]}" = "--" ] &&
   [[ "${argv[6]}" == "Consider and answer every numbered entry"*"plain turn"* ]]; then
  echo "ok   without mcp_servers a batch turn carries no overrides"
else
  printf 'FAIL without mcp_servers the argv changed: %s\n' "$(printf '%q ' "${argv[@]}")" >&2
  fail=1
fi

stop_worker
if [ "$fail" -ne 0 ]; then
  echo "--- worker stderr ---" >&2
  cat "$tmp/worker.err" >&2 || true
  exit 1
fi
echo "codex-exec-worker selftest passed"
