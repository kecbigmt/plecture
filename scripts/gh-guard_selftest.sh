#!/usr/bin/env bash
# Behavior tests for plugins/agent/{claude,codex}/bin/gh-guard: proves the
# shim denies merge/close before it is trusted as the opt-in gh_guard task
# input's mechanism, and that the two plugins' copies never silently
# diverge (each plugin ships its own copy — see either plugin's README —
# and nothing else re-derives one from the other).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
guard="$root/plugins/agent/claude/bin/gh-guard"
guard_codex_copy="$root/plugins/agent/codex/bin/gh-guard"

fail=0

# A stub "real gh" that records its argv and exits 0, so a passthrough case
# can be told apart from a denied one without calling the actual gh CLI.
stub_dir=$(mktemp -d)
trap 'rm -rf "$stub_dir"' EXIT
real_gh="$stub_dir/real-gh"
recorded="$stub_dir/recorded-args"
cat > "$real_gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$RECORDED_ARGS_FILE"
echo "real gh ran"
EOF
chmod +x "$real_gh"

run_guard() {
  GH_GUARD_REAL_GH="$real_gh" RECORDED_ARGS_FILE="$recorded" "$guard" "$@"
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

if ! diff -q "$guard" "$guard_codex_copy" >/dev/null; then
  echo "FAIL: agent/claude's and agent/codex's gh-guard copies have diverged" >&2
  fail=1
else
  echo "ok: agent/claude and agent/codex ship the same gh-guard"
fi

exit "$fail"
