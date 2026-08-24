#!/usr/bin/env bash
# Proves this plugin's activity probe reports evidence for a codex_exec
# turn even when the turn-boundary hooks alone would not: a single
# long-running `codex exec` call touches no hook until it ends, and its
# stdout never reaches the pane (it is redirected into the turn's own log
# file), so the pane fingerprint that covers this gap for an interactive
# agent (see plugins/tmux's pane.toml) cannot cover it here.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
activity="$here/codex-agent-activity"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export XDG_STATE_HOME="$tmp/xdg-state"
export PLECT_SESSION_NAME="selftest/session-1"
session="$PLECT_SESSION_NAME"
state_dir="$tmp/state"
mkdir -p "$state_dir/log"

fail=0
check() {
  local label="$1" want="$2" got="$3"
  if [ "$got" != "$want" ]; then
    echo "FAIL $label: got '$got', want '$want'" >&2
    fail=1
  else
    echo "ok   $label"
  fi
}

check_nonempty() {
  local label="$1" got="$2"
  if [ -z "$got" ]; then
    echo "FAIL $label: got empty output" >&2
    fail=1
  else
    echo "ok   $label"
  fi
}

"$activity" reset "$session"

# A fresh session with no turn yet reports no evidence — "no basis to judge"
# is not the same as "stalled".
out="$("$activity" probe "$session" "$state_dir")"
check "no evidence before any turn" "" "$out"

# A turn starting names its own log file before it writes anything to it —
# that name alone is evidence a turn began, with no hook call involved.
log_file="$state_dir/log/1000000000.jsonl"
: > "$log_file"
fp_start="$("$activity" probe "$session" "$state_dir" | jq -r .fingerprint)"
check_nonempty "turn start is evidence via its log file" "$fp_start"

# The same turn growing its log file — still no hook boundary crossed — is
# further evidence: the fingerprint must move again, or a turn that runs
# longer than the stall threshold reads as stalled while it is plainly
# working (the exact gap this probe exists to close for a headless runtime).
printf 'streamed output\n' >> "$log_file"
fp_grown="$("$activity" probe "$session" "$state_dir" | jq -r .fingerprint)"
if [ "$fp_grown" = "$fp_start" ]; then
  echo "FAIL mid-turn log growth changes the fingerprint: unchanged at '$fp_grown'" >&2
  fail=1
else
  echo "ok   mid-turn log growth changes the fingerprint"
fi

# The turn-boundary hooks still contribute independently of the log signal,
# and silence_expected still comes only from the hook half: a growing log
# is never grounds to call silence intended.
printf '{"hook_event_name":"UserPromptSubmit"}\n' | "$activity" working
working_envelope="$("$activity" probe "$session" "$state_dir")"
check "hook activity is not silence-expected" "false" "$(printf '%s' "$working_envelope" | jq -r .silence_expected)"

printf '{"hook_event_name":"Stop"}\n' | "$activity" waiting
waiting_envelope="$("$activity" probe "$session" "$state_dir")"
check "a completed turn's hook pardons silence" "true" "$(printf '%s' "$waiting_envelope" | jq -r .silence_expected)"

fp_before_hooks="$fp_grown"
fp_after_hooks="$(printf '%s' "$waiting_envelope" | jq -r .fingerprint)"
if [ "$fp_after_hooks" = "$fp_before_hooks" ]; then
  echo "FAIL a turn-boundary hook still changes the fingerprint: unchanged at '$fp_after_hooks'" >&2
  fail=1
else
  echo "ok   a turn-boundary hook still changes the fingerprint"
fi

# reset drops all of it, hook record and log-derived state alike (the log
# directory itself is this test's fixture, not something reset owns, so it
# is only the hook half that reset can clear).
"$activity" reset "$session"
rm -rf "${state_dir:?}/log"
mkdir -p "$state_dir/log"
out="$("$activity" probe "$session" "$state_dir")"
check "reset drops the record" "" "$out"

[ "$fail" -eq 0 ] || { echo "codex-agent-activity selftest failed" >&2; exit 1; }
echo "codex-agent-activity selftest passed"
