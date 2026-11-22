#!/usr/bin/env bash
# Proves check-all.sh's own behavior against a copy of it beside stubs that
# record each call: that --list names exactly what a run executes, that
# build-test runs once per module, that the base and branch reach the
# scripts that compare commits, and that a failure stops the run. Which
# scripts it must call is check-ci-scripts.sh's concern.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fixture=""
trap 'rm -rf "$fixture"' EXIT
fixture="$(mktemp -d)"
repo="$fixture/repo"
log="$fixture/out.log"
export RECORD="$fixture/record"

mkdir -p "$repo/scripts/ci"
cp "$root/scripts/check-all.sh" "$repo/scripts/"
stub() {
  cat >"$repo/scripts/$1" <<'EOF'
#!/usr/bin/env bash
name="scripts/${BASH_SOURCE[0]#*/scripts/}"
echo "$name $* HEAD_REF=${HEAD_REF:-}" >>"$RECORD"
[ "$name" != "${FAIL_SCRIPT:-}" ]
EOF
  chmod +x "$repo/scripts/$1"
}
for script in "$root"/scripts/ci/*.sh; do
  stub "ci/$(basename "$script")"
done
stub check-agent-config.sh
cat >"$repo/scripts/ci-changed-modules.sh" <<'EOF'
#!/usr/bin/env bash
[ "$FORCE_FULL_RUN" = true ] || exit 1
echo 'BUILD_TEST_MATRIX=["mod-a","mod-b"]'
EOF
chmod +x "$repo/scripts/ci-changed-modules.sh"

git -C "$repo" init -q -b main
commit() {
  git -C "$repo" add -A
  git -C "$repo" -c user.email=test@example.com -c user.name=test commit -q -m "$1"
}
commit base
base_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" update-ref refs/remotes/origin/main "$base_sha"

run_check() {
  rm -f "$RECORD"
  touch "$RECORD"
  "$repo/scripts/check-all.sh" "$@" >"$log" 2>&1
}

die() {
  echo "FAIL: $1" >&2
  echo "--- output" >&2
  cat "$log" >&2
  echo "--- record" >&2
  cat "$RECORD" >&2
  exit 1
}

recorded() { grep -qxF "$1" "$RECORD"; }

run_check "$base_sha" || die "check-all.sh failed with every script passing"
grep -q "check-all.sh: all checks passed" "$log" || die "no success line"
for module in mod-a mod-b; do
  recorded "scripts/ci/build-test.sh $module HEAD_REF=" || die "build-test did not run for $module"
done
recorded "scripts/ci/lint.sh $base_sha HEAD_REF=" || die "lint did not get the base it was given"
recorded "scripts/ci/comment-density.sh $base_sha HEAD HEAD_REF=main" ||
  die "comment-density did not get the base, the head, and the branch"
grep -q "^scripts/ci/web-browser-acceptance-browser.sh " "$RECORD" && die "the browser half of web-browser-acceptance ran"
recorded "scripts/check-agent-config.sh  HEAD_REF=" || die "the agent-config check did not run"
ran="$(cut -d' ' -f1 "$RECORD")"
echo "ok: check-all.sh runs each script once per call and every module through build-test"

touch "$repo/uncommitted"
run_check --list >/dev/null || die "--list failed with an uncommitted file"
[ "$(cat "$log")" = "$ran" ] || die "--list does not name exactly the scripts a run executes, in order"
[ -s "$RECORD" ] && die "--list ran a script"
rm "$repo/uncommitted"
echo "ok: --list names exactly what a run executes and runs nothing"

run_check || die "check-all.sh failed with its default base"
recorded "scripts/ci/lint.sh origin/main HEAD_REF=" || die "the default base is not origin/main"
echo "ok: the default base is origin/main"

git -C "$repo" checkout -q -b integration/x
run_check || die "check-all.sh failed on an aggregate branch"
recorded "scripts/ci/comment-density.sh origin/main HEAD HEAD_REF=integration/x" ||
  die "comment-density did not get the aggregate branch's name"
git -C "$repo" checkout -q main
echo "ok: the branch name reaches comment-density"

git -C "$repo" config status.showUntrackedFiles no
touch "$repo/uncommitted"
run_check && die "check-all.sh passed with an uncommitted file"
grep -q "FAILED: uncommitted changes" "$log" || die "the uncommitted-change refusal was not named"
[ -s "$RECORD" ] && die "a script ran despite uncommitted changes"
rm "$repo/uncommitted"
git -C "$repo" config --unset status.showUntrackedFiles
echo "ok: check-all.sh refuses to run against uncommitted changes"

FAIL_SCRIPT=scripts/ci/lint.sh run_check && die "check-all.sh passed despite a failing script"
grep -q "check-all.sh: FAILED: scripts/ci/lint.sh" "$log" || die "the failing script was not named"
recorded "scripts/check-agent-config.sh  HEAD_REF=" && die "a script after the failure ran"
echo "ok: check-all.sh stops at the first failing script and names it"
