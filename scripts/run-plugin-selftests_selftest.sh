#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runner="$root/scripts/run-plugin-selftests.sh"

fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/plugins/pluginA/scripts" "$fixture/plugins/pluginB/scripts" \
  "$fixture/plugins/pluginC/scripts" "$fixture/plugins/pluginD/scripts"

cat > "$fixture/plugins/pluginA/scripts/a_selftest.sh" <<'EOF'
#!/usr/bin/env bash
echo "ran pluginA selftest"
EOF

cat > "$fixture/plugins/pluginB/scripts/b_selftest.sh" <<'EOF'
#!/usr/bin/env bash
echo "ran pluginB selftest"
EOF

# The pluginC fixture intentionally has no selftest script (a legitimate,
# common case).

cat > "$fixture/plugins/pluginD/scripts/d_fails_selftest.sh" <<'EOF'
#!/usr/bin/env bash
echo "ran pluginD failing selftest"
exit 1
EOF

fail=0

# Requesting only pluginA must run pluginA's selftest and must not touch
# pluginB's, even though pluginB has one too — this is the isolation the
# owner directive requires.
out="$(PLUGIN_SELFTEST_ROOT="$fixture" "$runner" pluginA)"
if ! grep -q "ran pluginA selftest" <<< "$out"; then
  echo "FAIL: requesting pluginA did not run pluginA's own selftest" >&2
  fail=1
else
  echo "ok: requesting pluginA runs pluginA's own selftest"
fi
if grep -q "ran pluginB" <<< "$out"; then
  echo "FAIL: requesting pluginA also ran pluginB's selftest" >&2
  fail=1
else
  echo "ok: requesting pluginA does not run pluginB's selftest"
fi

# The pluginC fixture has no selftest script at all; requesting it alone
# (as an incidentally-affected plugin, not a full-repo scan) must not fail.
if ! PLUGIN_SELFTEST_ROOT="$fixture" "$runner" pluginC >"$fixture/c.log" 2>&1; then
  echo "FAIL: a selftest-less plugin, requested on its own, must not fail" >&2
  cat "$fixture/c.log" >&2
  fail=1
else
  echo "ok: a selftest-less plugin, requested on its own, does not fail"
fi

# REQUIRE_FOUND=true is the full-repo-scan canary: if nothing was found
# anywhere in the requested set, that's the naming convention breaking, not
# a legitimately empty plugin.
if REQUIRE_FOUND=true PLUGIN_SELFTEST_ROOT="$fixture" "$runner" pluginC >"$fixture/canary.log" 2>&1; then
  echo "FAIL: REQUIRE_FOUND=true with nothing found anywhere did not fail" >&2
  cat "$fixture/canary.log" >&2
  fail=1
else
  echo "ok: REQUIRE_FOUND=true fails when nothing was found anywhere"
fi

# REQUIRE_FOUND=true across the whole fixture (all three plugins) must
# pass, since pluginA/pluginB each have at least one selftest.
if ! REQUIRE_FOUND=true PLUGIN_SELFTEST_ROOT="$fixture" "$runner" pluginA pluginB pluginC >/dev/null 2>"$fixture/fullscan.log"; then
  echo "FAIL: REQUIRE_FOUND=true across the full fixture unexpectedly failed" >&2
  cat "$fixture/fullscan.log" >&2
  fail=1
else
  echo "ok: REQUIRE_FOUND=true across the full fixture passes"
fi

# A failing selftest script must fail the runner, not be swallowed.
if PLUGIN_SELFTEST_ROOT="$fixture" "$runner" pluginD >"$fixture/failprop.log" 2>&1; then
  echo "FAIL: a failing selftest script did not fail the runner" >&2
  cat "$fixture/failprop.log" >&2
  fail=1
else
  echo "ok: a failing selftest script fails the runner"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "run-plugin-selftests.sh does not correctly isolate plugin selftests." >&2
  exit 1
fi

echo "all run-plugin-selftests.sh isolation cases passed"
