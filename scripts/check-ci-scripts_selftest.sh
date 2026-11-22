#!/usr/bin/env bash
# Proves check-ci-scripts.sh against seeded defects. Each fixture is a small
# workflow whose aggregator job is ci-ok, beside a check-all.sh stub whose
# --list output stands in for the real one (check-all_selftest.sh proves
# that the real --list names what it runs).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-ci-scripts.sh"

work=""
trap 'rm -rf "$work"' EXIT
work="$(mktemp -d)"
log="$work/out.log"
repo="$work/repo"
mkdir -p "$repo/.github/workflows" "$repo/scripts/ci"
for name in a b web-browser-acceptance-browser; do
  printf '#!/usr/bin/env bash\n' >"$repo/scripts/ci/$name.sh"
done

default_list='scripts/ci/a.sh
scripts/ci/b.sh'

# The workflow comes on stdin; LISTED is what check-all.sh --list prints.
run_fixture() {
  cat >"$repo/.github/workflows/ci.yml"
  cat >"$repo/scripts/check-all.sh" <<EOF
#!/usr/bin/env bash
[ "\$1" = --list ] || exit 2
printf '%s\n' "${LISTED-$default_list}"
${LIST_EXIT:+exit $LIST_EXIT}
EOF
  chmod +x "$repo/scripts/check-all.sh"
  CI_SCRIPTS_CHECK_ROOT="$repo" "$checker" >"$log" 2>&1
}

expect_pass() {
  local label="$1"
  if ! run_fixture; then
    echo "FAIL: $label: the check failed" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $label"
}

# The location is the evidence that the finding points at the defect, not
# just that something somewhere failed.
expect_fail_at() {
  local label="$1" location="$2"
  if run_fixture; then
    echo "FAIL: $label: the check passed" >&2
    cat "$log" >&2
    exit 1
  fi
  if ! grep -Eq "^$location: " "$log"; then
    echo "FAIL: $label: no finding at $location" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $label"
}

ci=".github/workflows/ci\.yml"

expect_pass "setup actions followed by scripts/ci calls, with arguments and env, pass" <<'EOF'
on: pull_request
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      modules: ${{ steps.decide.outputs.modules }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - id: decide
        run: ./scripts/ci/a.sh "${{ github.event.pull_request.base.sha }}"
        env:
          HEAD_REF: ${{ github.head_ref }}
  build:
    name: build (${{ matrix.module }})
    needs: changes
    if: needs.changes.outputs.modules != '[]'
    runs-on: ubuntu-latest
    strategy:
      matrix:
        module: ${{ fromJson(needs.changes.outputs.modules) }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.0"
      - uses: actions/cache@v4
        with:
          path: ~/go
          key: k
      - name: build and test
        run: scripts/ci/b.sh "${{ matrix.module }}"
  browser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
      - run: ./scripts/ci/a.sh
      - run: |
          ./scripts/ci/web-browser-acceptance-browser.sh
  not-required:
    runs-on: ubuntu-latest
    steps:
      - run: echo anything goes in a job ci-ok does not need && false
  ci-ok:
    if: always()
    needs: [changes, build, browser]
    runs-on: ubuntu-latest
    steps:
      - run: echo "the aggregator's own step is not a job script"
EOF

browser_job='  browser:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/web-browser-acceptance-browser.sh'

expect_fail_at "a run step with an inline command" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a job script call chained to another command" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh && echo done
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a multi-line run block" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: |
          ./scripts/ci/a.sh
          ./scripts/ci/b.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a job script call run from another directory" "$ci:7" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
        working-directory: app
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a job script call run by another shell" "$ci:7" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
        shell: python
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "an action that is not setup" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: golangci/golangci-lint-action@v6
      - run: ./scripts/ci/a.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a setup action after a job script call" "$ci:7" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
      - uses: actions/setup-go@v5
      - run: ./scripts/ci/b.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

LISTED='scripts/ci/a.sh
scripts/ci/b.sh
scripts/ci/missing.sh' expect_fail_at "a call of a job script that does not exist" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/missing.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

LISTED='scripts/ci/a.sh' expect_fail_at "a job script check-all.sh never calls" "$ci:7" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
      - run: ./scripts/ci/b.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "the browser omission when no required job calls the browser script" \
  "scripts/check_ci_scripts\.py:[0-9]+" <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
  ci-ok:
    needs: [a]
    steps:
      - run: "true"
EOF

LISTED='scripts/ci/a.sh
scripts/ci/web-browser-acceptance-browser.sh' expect_fail_at \
  "the browser omission when check-all.sh calls the browser script after all" \
  "scripts/check_ci_scripts\.py:[0-9]+" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/a.sh
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a required job that does not exist" "$ci:8" <<EOF
on: pull_request
jobs:
$browser_job
  ci-ok:
    needs:
      - browser
      - missing
    steps:
      - run: "true"
EOF

expect_fail_at "a required job that calls a reusable workflow" "$ci:4" <<EOF
on: pull_request
jobs:
  a:
    uses: ./.github/workflows/other.yml
$browser_job
  ci-ok:
    needs: [a, browser]
    steps:
      - run: "true"
EOF

expect_fail_at "a job script call in a job that ci-ok needs only transitively" "$ci:6" <<EOF
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: make test
  b:
    needs: a
    runs-on: ubuntu-latest
    steps:
      - run: ./scripts/ci/b.sh
$browser_job
  ci-ok:
    needs: [b, browser]
    steps:
      - run: "true"
EOF

LIST_EXIT=3 expect_fail_at "a check-all.sh whose --list fails" "scripts/check-all\.sh" <<EOF
on: pull_request
jobs:
$browser_job
  ci-ok:
    needs: [browser]
    steps:
      - run: "true"
EOF
