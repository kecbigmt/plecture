#!/usr/bin/env bash
# Proves check-all.sh runs a workflow's required steps as CI would — job and
# step conditions, matrices fed by job outputs, expression substitution,
# exceptions — and stops naming the first failure. The fixture's own
# ci.yml stands in for the real one, so the real repository's suites are
# not re-run through it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-all.sh"

fixture=""
trap 'rm -rf "$fixture"' EXIT
fixture="$(mktemp -d)"
repo="$fixture/repo"
log="$fixture/out.log"
export RECORD="$fixture/record"

mkdir -p "$repo/.github/workflows" "$repo/mod-a" "$repo/mod-b" \
  "$repo/.claude/skills" "$repo/.agents/skills"
touch "$repo/mod-a/.keep" "$repo/mod-b/.keep"
printf 'placeholder\n' >"$repo/CLAUDE.md"
ln -s "CLAUDE.md" "$repo/AGENTS.md"

cat >"$repo/.github/workflows/ci.yml" <<'EOF'
name: CI
on: pull_request
env:
  GREETING: hello
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      modules: ${{ steps.decide.outputs.modules }}
      web: ${{ steps.decide.outputs.web }}
    steps:
      - uses: actions/checkout@v4
      - id: decide
        run: |
          echo 'modules=["mod-a","mod-b"]' >> "$GITHUB_OUTPUT"
          echo "web=false" >> "$GITHUB_OUTPUT"
  web:
    needs: changes
    if: needs.changes.outputs.web == 'true'
    runs-on: ubuntu-latest
    steps:
      - run: echo "web ran" >> "$RECORD"
  build:
    name: build (${{ matrix.module }})
    needs: changes
    runs-on: ubuntu-latest
    env:
      AGGREGATE: ${{ startsWith(github.head_ref, 'integration/') }}
    strategy:
      matrix:
        module: ${{ fromJson(needs.changes.outputs.modules) }}
    steps:
      - name: record
        run: echo "build $(basename "$PWD") $GREETING ${{ github.event.pull_request.base.sha }}..${{ github.event.pull_request.head.sha }}" >> "$RECORD"
        working-directory: ${{ matrix.module }}
      - name: aggregate only
        if: env.AGGREGATE == 'true'
        run: echo "aggregate ran" >> "$RECORD"
      # check-all-skip: while "sudo": installs system packages as root.
      - name: root only
        run: sudo false
  lint:
    runs-on: ubuntu-latest
    steps:
      - name: seeded check
        run: test ! -e seeded-defect
      - name: after the check
        run: echo "after" >> "$RECORD"
  unrequired:
    runs-on: ubuntu-latest
    steps:
      - run: echo "unrequired ran" >> "$RECORD"
  ci-ok:
    if: always()
    needs: [changes, web, build, lint]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

git -C "$repo" init -q -b main
git -C "$repo" add -A
git -C "$repo" -c user.email=test@example.com -c user.name=test commit -q -m base
base_sha="$(git -C "$repo" rev-parse HEAD)"

commit() {
  git -C "$repo" add -A
  git -C "$repo" -c user.email=test@example.com -c user.name=test commit -q -m "$1"
}

run_check() {
  rm -f "$RECORD"
  touch "$RECORD"
  CHECK_ALL_ROOT="$repo" "$checker" "$base_sha" >"$log" 2>&1
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

printf 'x\n' >"$repo/mod-a/change"
commit "a change on top of the base"
head_sha="$(git -C "$repo" rev-parse HEAD)"

run_check || die "check-all.sh failed against a clean fixture"
grep -q "check-all.sh: all checks passed" "$log" || die "no success line"
for module in mod-a mod-b; do
  recorded "build $module hello $base_sha..$head_sha" ||
    die "the matrix instance for $module did not run with the substituted base and head"
done
recorded "after" || die "a later step of a passing job did not run"
recorded "web ran" && die "a job whose if is false ran"
recorded "unrequired ran" && die "a job ci-ok does not require ran"
recorded "aggregate ran" && die "a step whose if is false ran"
grep -q "not run: build (mod-a) / root only: installs system packages as root." "$log" ||
  die "an excepted step was not reported with its reason"
grep -q "==> scripts/check-agent-config.sh" "$log" || die "the agent-config check did not run"
echo "ok: check-all.sh runs exactly the steps the workflow requires, as CI evaluates them"

git -C "$repo" checkout -q -b integration/x
run_check || die "check-all.sh failed on an aggregate branch"
recorded "aggregate ran" || die "a step whose if reads the head branch did not run on integration/x"
git -C "$repo" checkout -q main
echo "ok: the head branch reaches step conditions"

touch "$repo/uncommitted"
run_check && die "check-all.sh passed with an uncommitted file"
grep -q "FAILED: uncommitted changes" "$log" || die "the uncommitted-change refusal was not named"
[ -s "$RECORD" ] && die "a step ran despite uncommitted changes"
rm "$repo/uncommitted"
echo "ok: check-all.sh refuses to run against uncommitted changes"

touch "$repo/seeded-defect"
commit "seed a failing step"
run_check && die "check-all.sh passed despite a failing step"
grep -q "ci_steps: FAILED: lint / seeded check (.github/workflows/ci.yml:" "$log" ||
  die "the failing step was not named with its workflow line"
recorded "after" && die "a step after the failure ran"
grep -q "check-agent-config.sh" "$log" && die "a later check-all step ran after the failure"
echo "ok: check-all.sh stops at the first failing step and names it"
