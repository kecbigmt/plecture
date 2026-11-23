#!/usr/bin/env bash
# Proves check-all.sh's own behavior against a copy of it beside stubs that
# record each call: that build-test runs once per module, that the base and
# branch reach the scripts that compare commits, and that a failure stops
# the run.
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
recorded "scripts/ci/storage-contract.sh origin/main HEAD HEAD_REF=" || die "migration order did not keep CI's base when lint uses a custom base"
recorded "scripts/ci/comment-density.sh $base_sha HEAD HEAD_REF=main" ||
  die "comment-density did not get the base, the head, and the branch"
grep -q "^scripts/ci/web-browser-acceptance-browser.sh " "$RECORD" && die "the browser half of web-browser-acceptance ran"
recorded "scripts/check-agent-config.sh  HEAD_REF=" || die "the agent-config check did not run"
echo "ok: check-all.sh runs each script once per call and every module through build-test"

run_check || die "check-all.sh failed with its default base"
recorded "scripts/ci/lint.sh origin/main HEAD_REF=" || die "the default base is not origin/main"
recorded "scripts/ci/storage-contract.sh origin/main HEAD HEAD_REF=" || die "storage-contract did not get CI's migration order base and head"
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

storage_repo="$fixture/storage"
mkdir -p "$storage_repo/scripts/ci" "$storage_repo/app" "$fixture/bin"
cp "$root/scripts/ci/storage-contract.sh" "$storage_repo/scripts/ci/"
for name in check-migration-order check-migration-order_selftest; do
  cat >"$storage_repo/scripts/$name.sh" <<'EOF'
#!/usr/bin/env bash
[ "$PWD" = "$STORAGE_ROOT" ] || exit 1
echo "$(basename "$0") $*" >>"$RECORD"
[ "$(basename "$0")" != "${FAIL_SCRIPT:-}" ]
EOF
  chmod +x "$storage_repo/scripts/$name.sh"
done
cat >"$fixture/bin/go" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$fixture/bin/git" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$fixture/bin/"*
run_storage() {
  : >"$RECORD"
  STORAGE_ROOT="$storage_repo" PATH="$fixture/bin:$PATH" "$storage_repo/scripts/ci/storage-contract.sh" "$@" >"$log" 2>&1
}

run_storage || die "storage-contract failed for a push"
recorded "check-migration-order_selftest.sh " || die "push did not run the migration order selftest"
grep -q '^check-migration-order.sh ' "$RECORD" && die "push ran a diff-scoped migration check"
echo "ok: storage-contract runs the migration order selftest on a push"

run_storage base-tip head-tip || die "storage-contract failed for a pull request"
recorded "check-migration-order_selftest.sh " || die "pull request did not run the migration order selftest"
recorded "check-migration-order.sh base-tip head-tip" || die "migration order did not get the base branch tip and head"
echo "ok: storage-contract checks migration order against the pull request base and head"

FAIL_SCRIPT=check-migration-order.sh run_storage base-tip head-tip && die "storage-contract ignored a failing migration order check"
FAIL_SCRIPT=check-migration-order_selftest.sh run_storage base-tip head-tip && die "storage-contract ignored a failing migration order selftest"
grep -q '^check-migration-order.sh ' "$RECORD" && die "migration order check ran after its selftest failed"
echo "ok: storage-contract propagates migration order check and selftest failures"
