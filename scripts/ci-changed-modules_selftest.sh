#!/usr/bin/env bash
# Proves ci-changed-modules.sh reproduces every row of the trigger map
# documented in ci.yml, including the plugin-config reverse edge and the
# unknown-path fallback, before it is trusted to gate CI jobs.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mapper="$root/scripts/ci-changed-modules.sh"

fail=0

check() {
  local name="$1" input="$2" expected="$3"
  local actual
  actual="$(printf '%s' "$input" | "$mapper")"
  if [ "$actual" != "$expected" ]; then
    echo "FAIL: $name" >&2
    echo "  input:" >&2
    printf '%s\n' "$input" | sed 's/^/    /' >&2
    echo "  expected:" >&2
    printf '%s\n' "$expected" | sed 's/^/    /' >&2
    echo "  actual:" >&2
    printf '%s\n' "$actual" | sed 's/^/    /' >&2
    fail=1
    return
  fi
  echo "ok: $name"
}

check "docs-only diff -> lint set only, nothing else" \
  $'docs/design/foo.md\nCLAUDE.md' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=[]\nINTEGRATION_TEST=false\nREADME_VERIFY=false'

check "app/** -> app build-test + integration-test, not plugin build-tests" \
  'app/internal/task/executor.go' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["app"]\nINTEGRATION_TEST=true\nREADME_VERIFY=false'

check "plugins/X/src/** -> that plugin's build-test + integration-test, not app" \
  'plugins/github/src/internal/watcher/poll.go' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["plugins/github/src"]\nINTEGRATION_TEST=true\nREADME_VERIFY=false'

check "legacy-migration has no src/ subdir of its own" \
  'plugins/legacy-migration/internal/migrate/migrate.go' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["plugins/legacy-migration"]\nINTEGRATION_TEST=true\nREADME_VERIFY=false'

check "plugins/X/config|testdata/** -> app build-test (reverse edge), not integration-test" \
  'plugins/okf/config/tasks/pursue_goal.toml' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["app"]\nINTEGRATION_TEST=false\nREADME_VERIFY=false'

check "a config-only plugin's config/testdata still reaches app build-test" \
  'plugins/codex/testdata/effects/enqueue.json' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["app"]\nINTEGRATION_TEST=false\nREADME_VERIFY=false'

check "two plugins' src changed together -> only those two build-test entries" \
  $'plugins/okf/src/internal/foo.go\nplugins/slack/src/slack-adapter/cmd/main.go' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=["plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=false'

check "contracts/** -> full run (no scoping finer than the enforced boundary)" \
  'contracts/state/store.go' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check ".github/workflows/** -> full run (the checks themselves changed)" \
  '.github/workflows/ci.yml' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check "scripts/*.sh -> full run" \
  'scripts/gh-guard.sh' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check "README.md -> readme-verify only, no build-test/integration-test" \
  'README.md' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=[]\nINTEGRATION_TEST=false\nREADME_VERIFY=true'

check "an unmapped path (e.g. go.work) is not silently skipped -> full run" \
  'go.work' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check "root-level testdata/config-language is unmapped -> full run, not silently skipped" \
  'testdata/config-language/foo/case.toml' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check "a plugin's own plugin.toml is unmapped -> full run, not silently skipped" \
  'plugins/github/plugin.toml' \
  $'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'

check "no changed files -> nothing runs beyond the unconditional lint set" \
  '' \
  $'FULL_RUN=false\nBUILD_TEST_MATRIX=[]\nINTEGRATION_TEST=false\nREADME_VERIFY=false'

force_full_run_actual="$(FORCE_FULL_RUN=true "$mapper" < /dev/null)"
force_full_run_expected=$'FULL_RUN=true\nBUILD_TEST_MATRIX=["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]\nINTEGRATION_TEST=true\nREADME_VERIFY=true'
if [ "$force_full_run_actual" != "$force_full_run_expected" ]; then
  echo "FAIL: FORCE_FULL_RUN=true (used for push-to-main, which has no PR base to diff)" >&2
  echo "  expected:" >&2
  printf '%s\n' "$force_full_run_expected" | sed 's/^/    /' >&2
  echo "  actual:" >&2
  printf '%s\n' "$force_full_run_actual" | sed 's/^/    /' >&2
  fail=1
else
  echo "ok: FORCE_FULL_RUN=true short-circuits to a full run"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "ci-changed-modules.sh does not reproduce the documented trigger map." >&2
  exit 1
fi

echo "all ci-changed-modules.sh trigger-map cases passed"
