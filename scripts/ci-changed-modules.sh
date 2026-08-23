#!/usr/bin/env bash
# Maps a changed-file list (one path per line on stdin, repo-root-relative)
# to the CI jobs that need to run, per the dependency-derived trigger map:
# core never imports plugins and plugins never import core
# (check-provider-boundary.sh), and both sides depend only on contracts/*,
# so contracts/** changes are treated as "run everything" rather than
# scoped to individual contracts/* submodules — the map does not attempt a
# finer edge than the enforced boundary actually gives it.
#
# A plugin's shipped config/testdata is read directly off disk by app-side
# golden tests (app/internal/config, app/internal/task) that walk
# plugins/catalog.toml and each plugin's manifest — a reverse edge from
# plugin config back to the app build-test entry that a plain Go import
# graph would not surface.
#
# Any changed path that this map does not recognize falls back to a full
# run: skipping a job is only safe when its rationale traces to an enforced
# boundary or an explicit reverse edge above, not by default.
set -euo pipefail

# plugins/legacy-migration has no src/ subdirectory of its own — its module
# root doubles as what every other plugin calls src/.
BUILD_TEST_ALL='["app","contracts/atomicfile","contracts/channel-protocol","contracts/event","contracts/state","plugins/claude/src/channel-server","plugins/github/src","plugins/legacy-migration","plugins/okf/src","plugins/slack/src/slack-adapter"]'

if [ "${FORCE_FULL_RUN:-false}" = true ]; then
  echo "FULL_RUN=true"
  echo "BUILD_TEST_MATRIX=$BUILD_TEST_ALL"
  echo "INTEGRATION_TEST=true"
  echo "README_VERIFY=true"
  exit 0
fi

files="$(cat)"

any_match() {
  [ -n "$files" ] && echo "$files" | grep -Eq "$1"
}

CONTRACTS_RE='^contracts/'
APP_RE='^app/'
CLAUDE_SRC_RE='^plugins/claude/src/'
GITHUB_SRC_RE='^plugins/github/src/'
LEGACY_MIGRATION_RE='^plugins/legacy-migration/'
OKF_SRC_RE='^plugins/okf/src/'
SLACK_SRC_RE='^plugins/slack/src/slack-adapter/'
CLAUDE_CFG_RE='^plugins/claude/(config|testdata)/'
GITHUB_CFG_RE='^plugins/github/(config|testdata)/'
OKF_CFG_RE='^plugins/okf/(config|testdata)/'
SLACK_CFG_RE='^plugins/slack/(config|testdata)/'
CODEX_CFG_RE='^plugins/codex/(config|testdata)/'
TMUX_CFG_RE='^plugins/tmux/(config|testdata)/'
README_RE='^README\.md$'
FULL_RUN_TRIGGER_RE='^\.github/workflows/|^scripts/[^/]+\.sh$'
DOCS_OR_MD_RE='^docs/|\.md$'

KNOWN_RE="$CONTRACTS_RE|$APP_RE|$CLAUDE_SRC_RE|$GITHUB_SRC_RE|$LEGACY_MIGRATION_RE|$OKF_SRC_RE|$SLACK_SRC_RE|$CLAUDE_CFG_RE|$GITHUB_CFG_RE|$OKF_CFG_RE|$SLACK_CFG_RE|$CODEX_CFG_RE|$TMUX_CFG_RE|$FULL_RUN_TRIGGER_RE|$DOCS_OR_MD_RE"

unknown=false
if [ -n "$files" ] && echo "$files" | grep -Ev "$KNOWN_RE" | grep -q .; then
  unknown=true
fi

full_run=false
if any_match "$FULL_RUN_TRIGGER_RE" || any_match "$CONTRACTS_RE" || [ "$unknown" = true ]; then
  full_run=true
fi

if [ "$full_run" = true ]; then
  matrix="$BUILD_TEST_ALL"
else
  mods=()
  any_match "$APP_RE" && mods+=("app")
  any_match "$CLAUDE_CFG_RE" && mods+=("app")
  any_match "$GITHUB_CFG_RE" && mods+=("app")
  any_match "$OKF_CFG_RE" && mods+=("app")
  any_match "$SLACK_CFG_RE" && mods+=("app")
  any_match "$CODEX_CFG_RE" && mods+=("app")
  any_match "$TMUX_CFG_RE" && mods+=("app")
  any_match "$CLAUDE_SRC_RE" && mods+=("plugins/claude/src/channel-server")
  any_match "$GITHUB_SRC_RE" && mods+=("plugins/github/src")
  any_match "$LEGACY_MIGRATION_RE" && mods+=("plugins/legacy-migration")
  any_match "$OKF_SRC_RE" && mods+=("plugins/okf/src")
  any_match "$SLACK_SRC_RE" && mods+=("plugins/slack/src/slack-adapter")
  if [ "${#mods[@]}" -eq 0 ]; then
    matrix="[]"
  else
    matrix="$(printf '%s\n' "${mods[@]}" | sort -u | jq -R . | jq -s -c .)"
  fi
fi

integration_test=false
if [ "$full_run" = true ] || any_match "$APP_RE" || any_match "$CLAUDE_SRC_RE" \
  || any_match "$GITHUB_SRC_RE" || any_match "$LEGACY_MIGRATION_RE" \
  || any_match "$OKF_SRC_RE" || any_match "$SLACK_SRC_RE"; then
  integration_test=true
fi

readme_verify=false
if [ "$full_run" = true ] || any_match "$README_RE"; then
  readme_verify=true
fi

echo "FULL_RUN=$full_run"
echo "BUILD_TEST_MATRIX=$matrix"
echo "INTEGRATION_TEST=$integration_test"
echo "README_VERIFY=$readme_verify"
