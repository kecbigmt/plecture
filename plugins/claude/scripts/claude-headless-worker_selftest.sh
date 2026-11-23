#!/usr/bin/env bash
# Proves the worker's contract with `claude -p` and with the session inbox
# across its real process boundaries — a stubbed `claude`, a stubbed `plect`
# standing in for the inbox, and a background worker process — because every
# property here lives at one of those boundaries:
#
#   1. The first turn starts the conversation under the session id it was
#      handed, and every later turn resumes that same id, unless the first
#      turn failed before claude reported a session id.
#   2. An api_key_file's content reaches claude as ANTHROPIC_API_KEY only.
#   3. One batch becomes one prompt: every event numbered in order, shaped by
#      message_envelope, byte for byte.
#   4. A batch is acknowledged only after its turn ran and its reply was
#      published; a failure at either step leaves it for redelivery, and a
#      finished turn is never run twice for one batch.
#   5. A failed working-status write holds the batch without taking again.
#   6. Terminating the worker, mid-turn or idle, stops it and its claude. A
#      turn interrupted after its Stop hook reported is still run again.
#
# Which events a batch holds, redelivery and parking are core's and are
# specified by its own tests; the stub only replays that contract.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
worker="$here/claude-headless-worker"
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
cleanup() {
  stop_worker
  rm -rf "$tmp" 2>/dev/null || { sleep 0.3; rm -rf "$tmp"; }
}
trap cleanup EXIT

bin_dir="$tmp/bin"
mkdir -p "$bin_dir"

# The stub records its argv and the key it was given, then behaves as
# $STUB_MODE_FILE (read fresh per invocation, since the worker forks it long
# after this script's own exports) says: "ok" reports a finished turn through
# the real Stop hook and prints a result object carrying session_id,
# "fail-early" exits non-zero without one, "hang" writes its own pid and
# sleeps until killed, and "hang-after-stop" does the same after the Stop hook
# reported.
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
  hang-after-stop)
    printf '{"prompt_id":"%s","last_assistant_message":"unfinished"}' "$PLECT_BATCH_ID" | "$STUB_ACTIVITY" finish
    printf '%s' "$$" > "$STUB_HANG_PID"
    sleep 30
    ;;
esac
EOF
chmod +x "$bin_dir/claude"

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
export PLECT_FAIL_ACK="$tmp/fail-ack"
export PLECT_SESSION_NAME="selftest/worker-1"
export PLECT_CLAUDE_API_KEY_FILE="$tmp/api-key"
export XDG_STATE_HOME="$tmp/xdg-state"
unset PLECT_CLAUDE_MODEL PLECT_CLAUDE_EFFORT PLECT_CLAUDE_PERMISSION_MODE PLECT_CLAUDE_REPORT_REPLY
printf 'sk-selftest-key\n' > "$PLECT_CLAUDE_API_KEY_FILE"
echo ok > "$STUB_MODE_FILE"
: > "$STUB_ARGV"
: > "$STUB_KEYS"
: > "$PLECT_CALLS"

sid="11111111-2222-3333-4444-555555555555"
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
  echo ok > "$STUB_MODE_FILE"
  rm -f "$PLECT_FAIL_WORKING" "$PLECT_FAIL_WORKING_ONCE" "$PLECT_FAIL_PUBLISH" "$PLECT_FAIL_ACK"
}

start_worker() {
  FAKE_INBOX="$box" "$worker" "$state_dir" "$sid" "$tmp/mcp.json" "$tmp/hooks.json" 2>> "$tmp/worker.err" &
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
# A pass's health write, reply cleanup and tick all follow its
# acknowledgement, so the ack alone does not say the pass ended; the worker's
# next take after it does.
finished() {
  acked "$1" && awk '/^inbox ack /{a=NR} /^inbox take /{t=NR} END{exit !(a && t > a)}' "$PLECT_CALLS"
}
invocations() { wc -l < "$STUB_ARGV"; }
calls() { grep -c -- "$1" "$PLECT_CALLS" || true; }

check() {
  if eval "$2"; then
    echo "ok   $1"
  else
    echo "FAIL $1" >&2
    fail=1
  fi
}

expect_last_argv() {
  local got
  got="$(tail -n 1 "$STUB_ARGV")"
  case "$got" in
    *"$2"*) echo "ok   $1" ;;
    *) printf 'FAIL %s: got %q, want it to contain %q\n' "$1" "$got" "$2" >&2; fail=1 ;;
  esac
}

# --- conversation identity, key, health -----------------------------------
scenario identity
printf '%s' "99999999-8888-7777-6666-555555555555" > "$state_dir/started"
echo fail-early > "$STUB_MODE_FILE"
start_worker
hand_over t1 user.emit "turn one"
wait_until 'jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null 2>&1' || true
expect_last_argv "a first turn starts under the handed session id despite another id's marker" "--session-id $sid"

echo ok > "$STUB_MODE_FILE"
wait_until 'finished 1' || { echo "FAIL a failed turn's batch was not delivered again" >&2; fail=1; }
check "a batch whose turn failed is delivered again and acknowledged once" '[ "$(cat "$box/acked")" = "batch-1" ]'
hand_over t2 user.emit "turn two"
wait_until 'finished 2' || { echo "FAIL turn two was never acknowledged" >&2; fail=1; }
expect_last_argv "a turn after an early failure resumes the completed conversation" "--resume $sid"

check "the api key never appears on claude's command line" '! grep -q "sk-selftest-key" "$STUB_ARGV"'
check "api_key_file reaches claude as ANTHROPIC_API_KEY" '[ "$(tail -n 1 "$STUB_KEYS")" = "sk-selftest-key" ]'
check "health.json records the last turn's exit code" 'jq -e ".last_exit_code == 0 and .last_event_type == \"batch\"" "$state_dir/health.json" >/dev/null'
check "a turn sets the working status and ticks after its acknowledgement" '[ "$(calls "state set-message selftest/worker-1 working")" -ge 2 ] && [ "$(calls "^tick selftest/worker-1")" -eq 2 ]'

# --- one batch, one prompt ---------------------------------------------------
scenario prompt
hand_over a user.emit "first body"
hand_over b github.pull_request "" "https://example.test/pr/1"
hand_over c user.emit $'end\n\n'
start_worker
wait_until 'finished 1' || { echo "FAIL a three-event batch was never acknowledged" >&2; fail=1; }
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
wait_until 'finished 1' || { echo "FAIL an enveloped batch was never acknowledged" >&2; fail=1; }
check "message_envelope shapes each event" 'grep -qF "<user.emit|the body|summary of e|https://example.test/x>" "$STUB_PROMPT_FILE"'

# --- termination -------------------------------------------------------------
scenario terminate
echo hang > "$STUB_MODE_FILE"
rm -f "$STUB_HANG_PID"
start_worker
hand_over t4 user.emit "turn four"
wait_until '[ -s "$STUB_HANG_PID" ]' || { echo "FAIL the hanging turn never started" >&2; fail=1; }
hang_pid="$(cat "$STUB_HANG_PID" 2>/dev/null || true)"
kill -TERM "$worker_pid"
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
if [ -n "$hang_pid" ] && wait_until '! kill -0 "$hang_pid" 2>/dev/null' 30; then
  echo "ok   terminating the worker mid-turn terminates the running claude"
else
  echo "FAIL claude (pid ${hang_pid:-unknown}) outlived its terminated worker" >&2
  fail=1
fi
check "an interrupted batch stays unacknowledged" '[ ! -e "$box/acked" ] && [ -f "$box/batch.json" ]'
echo ok > "$STUB_MODE_FILE"
start_worker
wait_until 'finished 1' || { echo "FAIL an interrupted batch was not delivered again" >&2; fail=1; }
check "a restarted worker runs the interrupted batch again and acknowledges it once" '[ "$(grep -c "turn four" "$STUB_ARGV")" -eq 2 ] && [ "$(cat "$box/acked")" = "batch-1" ]'

scenario crash-after-stop
echo hang-after-stop > "$STUB_MODE_FILE"
rm -f "$STUB_HANG_PID"
start_worker
hand_over t5 user.emit "turn five"
wait_until '[ -s "$STUB_HANG_PID" ]' || { echo "FAIL the turn that hangs after its Stop hook never started" >&2; fail=1; }
check "the Stop hook kept a reply before claude exited" '[ -f "$state_dir/reply-batch-1.json" ]'
kill -TERM "$worker_pid"
wait "$worker_pid" 2>/dev/null || true
worker_pid=""
echo ok > "$STUB_MODE_FILE"
start_worker
wait_until 'finished 1' || { echo "FAIL a batch interrupted after its Stop hook was never acknowledged" >&2; fail=1; }
check "a turn interrupted after its Stop hook reported is run again" '[ "$(grep -c "turn five" "$STUB_ARGV")" -eq 2 ]'
check "only the finished turn's reply is published" '[ "$(calls "^event publish")" -eq 1 ] && ! grep -q "unfinished" "$PLECT_CALLS"'

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

# --- working status ------------------------------------------------------------
scenario status
touch "$PLECT_FAIL_WORKING"
start_worker
hand_over s1 user.emit "status turn"
wait_until 'jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null 2>&1' || true
sleep 1.5
check "a failed working status write starts no turn and reports failure" '[ "$(invocations)" -eq 0 ] && jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null'
check "a failed working status write does not take the batch again" '[ "$(jq ".deliveries" "$box/batch.json")" -eq 1 ]'
rm -f "$PLECT_FAIL_WORKING"
wait_until 'finished 1' || { echo "FAIL the held batch never ran" >&2; fail=1; }
check "the held batch runs once the status write succeeds" '[ "$(invocations)" -eq 1 ]'

scenario status-once
export STUB_HEALTH_FILE="$state_dir/health.json"
: > "$STUB_HEALTH_LOG"
touch "$PLECT_FAIL_WORKING_ONCE"
start_worker
hand_over s2 user.emit "retried status"
wait_until 'finished 1' || { echo "FAIL a batch behind a retried status write never ran" >&2; fail=1; }
check "health passes during a turn that follows a retried working write" '[ "$(cat "$STUB_HEALTH_LOG")" = "0" ]'

# --- agent failure -------------------------------------------------------------
scenario poison
: > "$STUB_HEALTH_LOG"
export STUB_HEALTH_FILE="$state_dir/health.json"
echo fail-early > "$STUB_MODE_FILE"
start_worker
hand_over p1 user.emit "poison turn"
wait_until '[ -s "$box/parked" ]' 250 || { echo "FAIL a failing batch was never given up" >&2; fail=1; }
check "a failing batch is run once per delivery and no more" '[ "$(grep -c "poison turn" "$STUB_ARGV")" -eq 3 ]'
check "health recovers before every retry of the agent" '[ "$(sort -u "$STUB_HEALTH_LOG")" = "0" ]'
echo ok > "$STUB_MODE_FILE"
hand_over p2 user.emit "after poison"
wait_until 'finished 1' || { echo "FAIL a parked batch blocked newer work" >&2; fail=1; }
check "the worker moves on to the next batch after core parks one" '[ "$(cat "$box/acked")" = "batch-2" ]'
export STUB_HEALTH_FILE=""

# --- reply publication -----------------------------------------------------------
scenario publish
touch "$PLECT_FAIL_PUBLISH"
start_worker
hand_over r1 user.emit "publish turn"
wait_until '[ "$(jq ".deliveries" "$box/batch.json" 2>/dev/null || echo 0)" -ge 2 ]' || { echo "FAIL a batch with a failed publication was not delivered again" >&2; fail=1; }
check "a failed reply publication does not acknowledge the batch" '[ ! -e "$box/acked" ] && jq -e ".last_exit_code == 1" "$state_dir/health.json" >/dev/null'
rm -f "$PLECT_FAIL_PUBLISH"
wait_until 'finished 1' || { echo "FAIL the reply publication was not retried" >&2; fail=1; }
check "a publication retry does not run the agent again" '[ "$(invocations)" -eq 1 ]'
check "the reply carries the batch id in its message id" 'grep -q -- "--meta message_id=selftest/worker-1/batch-1" "$PLECT_CALLS"'
check "the kept reply is dropped after the acknowledgement" '! ls "$state_dir"/reply-* >/dev/null 2>&1'

scenario ack
touch "$PLECT_FAIL_ACK"
start_worker
hand_over k1 user.emit "ack turn"
wait_until '[ "$(jq ".deliveries" "$box/batch.json" 2>/dev/null || echo 0)" -ge 2 ]' || { echo "FAIL a batch with a failed acknowledgement was not delivered again" >&2; fail=1; }
rm -f "$PLECT_FAIL_ACK"
wait_until 'finished 1' || { echo "FAIL the acknowledgement was not retried" >&2; fail=1; }
check "an acknowledgement retry neither runs the agent nor publishes again" '[ "$(invocations)" -eq 1 ] && [ "$(calls "^event publish")" -eq 1 ]'

scenario opt-out
export PLECT_CLAUDE_REPORT_REPLY=false
start_worker
hand_over o1 user.emit "quiet turn"
wait_until 'finished 1' || { echo "FAIL an opted-out batch was never acknowledged" >&2; fail=1; }
check "a runtime that publishes no reply still acknowledges its batch" '[ "$(invocations)" -eq 1 ] && [ "$(calls "^event publish")" -eq 0 ]'
unset PLECT_CLAUDE_REPORT_REPLY

stop_worker
if [ "$fail" -ne 0 ]; then
  echo "--- worker stderr ---" >&2
  cat "$tmp/worker.err" >&2 || true
  exit 1
fi
echo "claude-headless-worker selftest passed"
