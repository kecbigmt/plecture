#!/usr/bin/env bash
# Behavior tests for plugins/github/scripts/gh-app-guard: proves it composes
# token minting and merge/close denial in the deny-before-mint order the
# wrapper's own header comment claims, and that GH_TOKEN reaches only the
# delegated real-gh child process. gh-guard-lib.sh's own deny logic is
# exercised exhaustively by gh-guard_selftest.sh; this file only proves the
# composition, not the deny rules a second time.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
guard="$root/plugins/github/scripts/gh-app-guard"

fail=0

stub_dir=$(mktemp -d)
trap 'rm -rf "$stub_dir"' EXIT

real_gh="$stub_dir/real-gh"
recorded_gh_args="$stub_dir/recorded-gh-args"
recorded_gh_token="$stub_dir/recorded-gh-token"
cat > "$real_gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$RECORDED_GH_ARGS_FILE"
printf '%s' "${GH_TOKEN-}" > "$RECORDED_GH_TOKEN_FILE"
echo "real gh ran"
EOF
chmod +x "$real_gh"

# A stand-in gh-app-token: records its own argv, then answers per
# $TOKEN_BIN_ANSWER — either a token to print, or "fail:<message>" to exit 1
# after printing <message> to stderr (standing in for a redacted failure
# gh-app-token would produce for real).
token_bin="$stub_dir/gh-app-token"
recorded_token_args="$stub_dir/recorded-token-args"
cat > "$token_bin" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$RECORDED_TOKEN_ARGS_FILE"
case "$TOKEN_BIN_ANSWER" in
  fail:*)
    echo "${TOKEN_BIN_ANSWER#fail:}" >&2
    exit 1
    ;;
  *)
    printf '%s\n' "$TOKEN_BIN_ANSWER"
    ;;
esac
EOF
chmod +x "$token_bin"

private_key_path="$stub_dir/app.pem"
: > "$private_key_path"
cache_path="$stub_dir/cache.json"

run_guard() {
  GH_GUARD_REAL_GH="$real_gh" \
    GH_APP_GUARD_TOKEN_BIN="$token_bin" \
    GH_APP_GUARD_APP_ID="12345" \
    GH_APP_GUARD_PRIVATE_KEY_PATH="$private_key_path" \
    GH_APP_GUARD_CACHE_PATH="$cache_path" \
    RECORDED_GH_ARGS_FILE="$recorded_gh_args" \
    RECORDED_GH_TOKEN_FILE="$recorded_gh_token" \
    RECORDED_TOKEN_ARGS_FILE="$recorded_token_args" \
    TOKEN_BIN_ANSWER="${TOKEN_BIN_ANSWER:-ghs_stub_token}" \
    "$guard" "$@" < /dev/null
}

reset_records() {
  rm -f "$recorded_gh_args" "$recorded_gh_token" "$recorded_token_args"
}

expect_denied_without_minting() {
  local desc="$1"
  shift
  reset_records
  if out=$(run_guard "$@" 2>&1); then
    echo "FAIL: $desc — guard allowed it: $out" >&2
    fail=1
    return
  fi
  if [ -e "$recorded_token_args" ]; then
    echo "FAIL: $desc — gh-app-token ran despite the deny (deny must precede minting)" >&2
    fail=1
    return
  fi
  if [ -e "$recorded_gh_args" ]; then
    echo "FAIL: $desc — real gh ran despite the deny" >&2
    fail=1
    return
  fi
  echo "ok: $desc denied before minting"
}

expect_passthrough_with_token() {
  local desc="$1"
  shift
  reset_records
  if ! out=$(run_guard "$@" 2>&1); then
    echo "FAIL: $desc — guard refused a call it should have passed through: $out" >&2
    fail=1
    return
  fi
  if [ ! -e "$recorded_gh_args" ]; then
    echo "FAIL: $desc — real gh never ran" >&2
    fail=1
    return
  fi
  if [ "$(cat "$recorded_gh_args")" != "$(printf '%s\n' "$@")" ]; then
    echo "FAIL: $desc — real gh received different args than passed to the guard" >&2
    fail=1
    return
  fi
  if [ "$(cat "$recorded_gh_token")" != "$TOKEN_BIN_ANSWER" ]; then
    echo "FAIL: $desc — real gh's GH_TOKEN was $(cat "$recorded_gh_token"), want $TOKEN_BIN_ANSWER" >&2
    fail=1
    return
  fi
  echo "ok: $desc passed through with the minted token"
}

TOKEN_BIN_ANSWER="ghs_stub_token"

expect_denied_without_minting "pr merge" pr merge 123
expect_denied_without_minting "issue close" issue close 123
expect_denied_without_minting "api -X PUT .../merge" api -X PUT repos/o/r/pulls/1/merge

expect_passthrough_with_token "issue view (read)" issue view 123
expect_passthrough_with_token "pr view (read)" pr view 123
expect_passthrough_with_token "api GET" api repos/o/r/issues/1

# Given the token mint fails, the wrapper fails loudly with the helper's own
# (already redacted) message, real gh never runs, and nothing leaks onto
# stdout.
reset_records
TOKEN_BIN_ANSWER="fail:gh-app-token: private key unreadable"
out=""
err=""
if out=$(run_guard issue view 123 2>/tmp/gh-app-guard-selftest-mintfail.err); then
  echo "FAIL: mint failure — guard allowed the call through: $out" >&2
  fail=1
fi
err=$(cat /tmp/gh-app-guard-selftest-mintfail.err)
if [ -e "$recorded_gh_args" ]; then
  echo "FAIL: mint failure — real gh ran despite the mint failing" >&2
  fail=1
fi
if [ -n "$out" ]; then
  echo "FAIL: mint failure — stdout carried output ($out), want nothing" >&2
  fail=1
fi
if [[ "$err" != *"private key unreadable"* ]]; then
  echo "FAIL: mint failure — wrapper stderr ($err) did not surface gh-app-token's message" >&2
  fail=1
else
  echo "ok: mint failure surfaces gh-app-token's own message, denies the call, and prints nothing to stdout"
fi
TOKEN_BIN_ANSWER="ghs_stub_token"

# Optional installation/owner/repo/base-url env vars, when set, must reach
# gh-app-token as flags; left unset, they must not appear at all (an empty
# --installation-id would tell gh-app-token "the operator asked for
# installation id \"\"", not "let owner/repo resolve it").
reset_records
GH_APP_GUARD_INSTALLATION_ID="999" run_guard issue view 123 >/dev/null 2>&1 || true
if [[ "$(cat "$recorded_token_args" 2>/dev/null || true)" != *"--installation-id"$'\n'"999"* ]]; then
  echo "FAIL: --installation-id was not forwarded to gh-app-token" >&2
  fail=1
else
  echo "ok: GH_APP_GUARD_INSTALLATION_ID forwarded as --installation-id"
fi

reset_records
run_guard issue view 123 >/dev/null 2>&1 || true
if grep -q -- "--installation-id" "$recorded_token_args" 2>/dev/null; then
  echo "FAIL: --installation-id was forwarded despite GH_APP_GUARD_INSTALLATION_ID being unset" >&2
  fail=1
else
  echo "ok: --installation-id omitted when GH_APP_GUARD_INSTALLATION_ID is unset"
fi

# Misconfiguration must fail loud, same discipline as gh-guard.
if GH_GUARD_REAL_GH="" GH_APP_GUARD_TOKEN_BIN="$token_bin" GH_APP_GUARD_APP_ID="1" \
  GH_APP_GUARD_PRIVATE_KEY_PATH="$private_key_path" GH_APP_GUARD_CACHE_PATH="$cache_path" \
  "$guard" issue view 123 >/tmp/gh-app-guard-selftest-unset.log 2>&1; then
  echo "FAIL: guard ran with GH_GUARD_REAL_GH unset" >&2
  fail=1
else
  echo "ok: refuses to run with GH_GUARD_REAL_GH unset"
fi

if GH_GUARD_REAL_GH="$guard" GH_APP_GUARD_TOKEN_BIN="$token_bin" GH_APP_GUARD_APP_ID="1" \
  GH_APP_GUARD_PRIVATE_KEY_PATH="$private_key_path" GH_APP_GUARD_CACHE_PATH="$cache_path" \
  "$guard" issue view 123 >/tmp/gh-app-guard-selftest-self.log 2>&1; then
  echo "FAIL: guard ran with GH_GUARD_REAL_GH pointing at itself" >&2
  fail=1
else
  echo "ok: refuses to recurse when GH_GUARD_REAL_GH resolves back to itself"
fi

if GH_GUARD_REAL_GH="$real_gh" GH_APP_GUARD_APP_ID="1" \
  GH_APP_GUARD_PRIVATE_KEY_PATH="$private_key_path" GH_APP_GUARD_CACHE_PATH="$cache_path" \
  "$guard" issue view 123 >/tmp/gh-app-guard-selftest-notoken.log 2>&1; then
  echo "FAIL: guard ran with GH_APP_GUARD_TOKEN_BIN unset" >&2
  fail=1
else
  echo "ok: refuses to run with GH_APP_GUARD_TOKEN_BIN unset"
fi

exit "$fail"
