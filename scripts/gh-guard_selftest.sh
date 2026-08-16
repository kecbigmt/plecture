#!/usr/bin/env bash
# Behavior tests for plugins/session/runtime/scripts/gh-guard: proves the
# shim denies merge/close before it is trusted as the opt-in gh_guard task
# input's mechanism shared by the claude, codex, and codex_exec tasks.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
guard="$root/plugins/session/runtime/scripts/gh-guard"

fail=0

# A stub "real gh" that records its argv and exits 0, so a passthrough case
# can be told apart from a denied one without calling the actual gh CLI.
stub_dir=$(mktemp -d)
trap 'rm -rf "$stub_dir"' EXIT
real_gh="$stub_dir/real-gh"
recorded="$stub_dir/recorded-args"
recorded_stdin="$stub_dir/recorded-stdin"
cat > "$real_gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$RECORDED_ARGS_FILE"
cat > "$RECORDED_STDIN_FILE"
echo "real gh ran"
EOF
chmod +x "$real_gh"

run_guard() {
  GH_GUARD_REAL_GH="$real_gh" RECORDED_ARGS_FILE="$recorded" RECORDED_STDIN_FILE="$recorded_stdin" "$guard" "$@" < /dev/null
}

run_guard_with_stdin() {
  local stdin_content="$1"
  shift
  printf '%s' "$stdin_content" \
    | GH_GUARD_REAL_GH="$real_gh" RECORDED_ARGS_FILE="$recorded" RECORDED_STDIN_FILE="$recorded_stdin" "$guard" "$@"
}

expect_denied() {
  local desc="$1"
  shift
  rm -f "$recorded"
  if out=$(run_guard "$@" 2>&1); then
    echo "FAIL: $desc — guard allowed it (real gh not reached should have failed): $out" >&2
    fail=1
    return
  fi
  if [ -e "$recorded" ]; then
    echo "FAIL: $desc — real gh ran despite the deny" >&2
    fail=1
    return
  fi
  echo "ok: $desc denied"
}

expect_passthrough() {
  local desc="$1"
  shift
  rm -f "$recorded"
  if ! out=$(run_guard "$@" 2>&1); then
    echo "FAIL: $desc — guard refused a call it should have passed through: $out" >&2
    fail=1
    return
  fi
  if [ ! -e "$recorded" ]; then
    echo "FAIL: $desc — real gh never ran" >&2
    fail=1
    return
  fi
  if [ "$(cat "$recorded")" != "$(printf '%s\n' "$@")" ]; then
    echo "FAIL: $desc — real gh received different args than passed to the guard" >&2
    fail=1
    return
  fi
  echo "ok: $desc passed through"
}

# --input -: the body arrives on the guard's own stdin, not argv, so these
# variants drive run_guard_with_stdin instead and (for the passthrough case)
# also confirm the buffered body actually reached the real gh unchanged —
# the fd-replay after inspection is exactly the part a naive fix could get
# wrong (denying/allowing correctly by luck while still starving the real
# call of its body).
expect_denied_stdin() {
  local desc="$1" stdin_content="$2"
  shift 2
  rm -f "$recorded"
  if out=$(run_guard_with_stdin "$stdin_content" "$@" 2>&1); then
    echo "FAIL: $desc — guard allowed it (real gh not reached should have failed): $out" >&2
    fail=1
    return
  fi
  if [ -e "$recorded" ]; then
    echo "FAIL: $desc — real gh ran despite the deny" >&2
    fail=1
    return
  fi
  echo "ok: $desc denied"
}

expect_passthrough_stdin() {
  local desc="$1" stdin_content="$2"
  shift 2
  rm -f "$recorded" "$recorded_stdin"
  if ! out=$(run_guard_with_stdin "$stdin_content" "$@" 2>&1); then
    echo "FAIL: $desc — guard refused a call it should have passed through: $out" >&2
    fail=1
    return
  fi
  if [ ! -e "$recorded" ]; then
    echo "FAIL: $desc — real gh never ran" >&2
    fail=1
    return
  fi
  if [ "$(cat "$recorded_stdin")" != "$stdin_content" ]; then
    echo "FAIL: $desc — real gh received a different body than the original stdin" >&2
    fail=1
    return
  fi
  echo "ok: $desc passed through with body intact"
}

expect_denied  "pr merge"                                 pr merge 123
expect_denied  "issue close"                               issue close 123
expect_denied  "pr close"                                   pr close 123
expect_denied  "api -X PUT .../merge (split flag)"          api -X PUT repos/o/r/pulls/1/merge
expect_denied  "api --method=PUT .../merge (joined flag)"   api --method=PUT repos/o/r/pulls/1/merge
expect_denied  "api -XPUT .../merge (glued flag)"           api -XPUT repos/o/r/pulls/1/merge
expect_denied  "api -X PATCH with state=closed body arg"    api -X PATCH repos/o/r/issues/1 -f state=closed
expect_denied  "api -X PATCH with JSON state:closed body"   api -X PATCH repos/o/r/issues/1 -f 'body={"state":"closed"}'

expect_passthrough "issue view (read)"                      issue view 123
expect_passthrough "pr view (read)"                          pr view 123
expect_passthrough "api GET (default method)"                 api repos/o/r/issues/1
expect_passthrough "api -X PATCH without closed body"        api -X PATCH repos/o/r/issues/1 -f title=x
expect_passthrough "api -X PUT to a non-merge path"           api -X PUT repos/o/r/issues/1/labels

# --input <file>: the body bypasses argv entirely (gh reads it straight from
# the file), so a closed state hidden there must still be caught.
closed_body_file="$stub_dir/closed-body.json"
printf '%s' '{"state":"closed"}' > "$closed_body_file"
open_body_file="$stub_dir/open-body.json"
printf '%s' '{"title":"x"}' > "$open_body_file"

expect_denied      "api -X PATCH --input <file> (split) with closed body"    api -X PATCH repos/o/r/issues/1 --input "$closed_body_file"
expect_denied      "api -X PATCH --input=<file> (joined) with closed body"   api -X PATCH repos/o/r/issues/1 --input="$closed_body_file"
expect_passthrough "api -X PATCH --input <file> without closed body"         api -X PATCH repos/o/r/issues/1 --input "$open_body_file"

# Same JSON document, ordinary alternative whitespace/formatting — a
# substring-literal match would miss these; the jq-based check must not.
spaced_body_file="$stub_dir/spaced-body.json"
printf '%s' '{ "state" : "closed" }' > "$spaced_body_file"
multiline_body_file="$stub_dir/multiline-body.json"
printf '%s\n' '{' '  "state": "closed"' '}' > "$multiline_body_file"
expect_denied "api -X PATCH --input <file> with spaced JSON closed body"     api -X PATCH repos/o/r/issues/1 --input "$spaced_body_file"
expect_denied "api -X PATCH --input <file> with multiline JSON closed body"  api -X PATCH repos/o/r/issues/1 --input "$multiline_body_file"

# --input -: the body arrives on stdin instead of a file — same bypass, plus
# the guard must buffer-and-replay it so the real gh still receives the body
# it was never allowed to read directly (see run_guard_with_stdin above).
expect_denied_stdin      "api -X PATCH --input - with closed body on stdin"          '{"state":"closed"}'    api -X PATCH repos/o/r/issues/1 --input -
expect_denied_stdin      "api -X PATCH --input - with spaced JSON closed body"       '{ "state" : "closed" }' api -X PATCH repos/o/r/issues/1 --input -
expect_passthrough_stdin "api -X PATCH --input - without closed body on stdin"       '{"title":"x"}'         api -X PATCH repos/o/r/issues/1 --input -

# Misconfiguration must fail loud, never silently fall back to an unguarded
# real gh: an unset/self-referential/non-executable GH_GUARD_REAL_GH is a
# wiring bug, not a "guard doesn't apply here" case.
if GH_GUARD_REAL_GH="" "$guard" issue view 123 >/tmp/gh-guard-selftest-unset.log 2>&1; then
  echo "FAIL: guard ran with GH_GUARD_REAL_GH unset" >&2
  fail=1
else
  echo "ok: refuses to run with GH_GUARD_REAL_GH unset"
fi

if GH_GUARD_REAL_GH="$guard" "$guard" issue view 123 >/tmp/gh-guard-selftest-self.log 2>&1; then
  echo "FAIL: guard ran with GH_GUARD_REAL_GH pointing at itself" >&2
  fail=1
else
  echo "ok: refuses to recurse when GH_GUARD_REAL_GH resolves back to itself"
fi

not_exec="$stub_dir/not-executable"
: > "$not_exec"
if GH_GUARD_REAL_GH="$not_exec" "$guard" issue view 123 >/tmp/gh-guard-selftest-noexec.log 2>&1; then
  echo "FAIL: guard ran with a non-executable GH_GUARD_REAL_GH" >&2
  fail=1
else
  echo "ok: refuses to run with a non-executable GH_GUARD_REAL_GH"
fi

exit "$fail"
