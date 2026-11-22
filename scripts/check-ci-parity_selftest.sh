#!/usr/bin/env bash
# Proves check-ci-parity.sh against seeded defects. Each fixture is a small
# workflow whose aggregator job is `ci-ok`; the run half of the same runner
# (executing steps, matrices, outputs) is proved by check-all_selftest.sh.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-ci-parity.sh"

work=""
trap 'rm -rf "$work"' EXIT
work="$(mktemp -d)"
log="$work/out.log"

run_fixture() {
  mkdir -p "$work/repo/.github/workflows"
  cat >"$work/repo/.github/workflows/ci.yml"
  CI_PARITY_CHECK_ROOT="$work/repo" "$checker" >"$log" 2>&1
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

# The line is the evidence that the finding points at the defect, not just
# that something somewhere failed.
expect_fail_at() {
  local label="$1" line="$2"
  if run_fixture; then
    echo "FAIL: $label: the check passed" >&2
    cat "$log" >&2
    exit 1
  fi
  if ! grep -q "^\.github/workflows/ci\.yml:$line: " "$log"; then
    echo "FAIL: $label: no finding at ci.yml:$line" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $label"
}

expect_pass "a workflow using every construct the runner evaluates passes" <<'EOF'
name: CI
on: pull_request
env:
  GO_VERSION: "1.0"
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.decide.outputs.matrix }}
      web: ${{ steps.decide.outputs.web }}
    steps:
      - uses: actions/checkout@v4
      - id: decide
        run: echo "matrix=[\"a\"]" >> "$GITHUB_OUTPUT"
  build:
    name: build (${{ matrix.module }})
    needs: changes
    if: needs.changes.outputs.matrix != '[]' && github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    env:
      AGGREGATE: ${{ startsWith(github.head_ref, 'integration/') || contains(github.ref, 'x') }}
    strategy:
      fail-fast: false
      matrix:
        module: ${{ fromJson(needs.changes.outputs.matrix) }}
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          key: ${{ hashFiles('**/go.sum') }}-${{ runner.os }}
      - name: test
        if: env.AGGREGATE != 'true'
        run: echo "${{ github.event.pull_request.base.sha }} ${{ github.event.pull_request.head.sha }}"
        working-directory: ${{ matrix.module }}
      # check-all-skip: while "sudo": installs system packages as root.
      - name: install
        run: sudo apt-get install -y ${{ unknownFunction() }}
  not-required:
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ anything.goes.here }}
  ci-ok:
    if: always()
    needs: [changes, build]
    runs-on: ubuntu-latest
    steps:
      - run: echo "${{ toJson(needs) }}"
EOF

expect_fail_at "an expression function the runner cannot evaluate" 7 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - name: uses an unknown function
        run: echo ${{ format('{0}', 'x') }}
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a github context field with no local value" 6 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - if: github.event.pull_request.draft == false
        run: "true"
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a needs reference to a job the job does not need" 4 <<'EOF'
on: pull_request
jobs:
  a:
    if: needs.b.outputs.x == 'true'
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  b:
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  ci-ok:
    needs: [a, b]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a matrix key the job does not declare" 6 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ matrix.module }}
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an action that is not environment setup" 6 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: golangci/golangci-lint-action@v6
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a step key the runner does not model" 7 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: print("x")
        shell: python
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a job that calls a reusable workflow" 4 <<'EOF'
on: pull_request
jobs:
  a:
    uses: ./.github/workflows/other.yml
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a job that needs a job declared after it" 4 <<'EOF'
on: pull_request
jobs:
  a:
    needs: b
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  b:
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an exception whose removal condition holds" 7 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      # check-all-skip: while "sudo": installs system packages as root.
      - name: no longer needs root
        run: ./install.sh
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an exception on a job ci-ok does not require" 7 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      # check-all-skip: while "sudo": installs system packages as root.
      - name: install
        run: sudo ./install.sh
  b:
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  ci-ok:
    needs: [b]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an exception that annotates no step" 4 <<'EOF'
on: pull_request
jobs:
  # check-all-skip: while "sudo": a job key is not a step.
  a:
    runs-on: ubuntu-latest
    steps:
      - run: sudo ./install.sh
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an exception with no reason" 7 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      # check-all-skip: while "sudo":
      - name: install
        run: sudo ./install.sh
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an output of an action step, which the runner never populates" 8 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - id: setup
        uses: actions/setup-go@v5
      - if: steps.setup.outputs.cache-hit != 'true'
        run: ./slow-path.sh
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "an output of an excepted step, which the runner never populates" 10 <<'EOF'
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      # check-all-skip: while "sudo": installs system packages as root.
      - id: install
        run: sudo ./install.sh >> "$GITHUB_OUTPUT"
      - name: uses the skipped step's output
        if: steps.install.outputs.ok == 'true'
        run: ./test.sh
  ci-ok:
    needs: [a]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF

expect_fail_at "a required job that does not exist" 4 <<'EOF'
on: pull_request
jobs:
  ci-ok:
    needs: [missing]
    runs-on: ubuntu-latest
    steps:
      - run: "true"
EOF
