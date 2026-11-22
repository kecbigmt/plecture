#!/usr/bin/env bash
# Proves check-actionlint.sh against seeded defects in a fixture repository:
# an expression naming an undefined property fails and is named, and so
# does a shellcheck warning inside a `run:` script.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-actionlint.sh"

fixture="$(mktemp -d)"
log="$fixture.log"
trap 'rm -rf "$fixture" "$log"' EXIT
git -C "$fixture" init -q
mkdir -p "$fixture/.github/workflows"

# workflow writes ci.yml with one job whose single step is stdin.
workflow() {
  {
    cat <<'EOF'
name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
EOF
    cat
  } > "$fixture/.github/workflows/ci.yml"
}

run_check() {
  ACTIONLINT_CHECK_ROOT="$fixture" "$checker" > "$log" 2>&1
}

expect_pass() {
  if ! run_check; then
    echo "FAIL: $1: checker failed" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $1"
}

expect_fail_naming() {
  local name="$1"
  shift
  if run_check; then
    echo "FAIL: $name: checker passed" >&2
    cat "$log" >&2
    exit 1
  fi
  local want
  for want in "$@"; do
    if ! grep -qF -- "$want" "$log"; then
      echo "FAIL: $name: output does not name '$want'" >&2
      cat "$log" >&2
      exit 1
    fi
  done
  echo "ok: $name"
}

workflow <<'EOF'
      - run: echo "$GITHUB_SHA"
EOF
expect_pass "a clean workflow passes"

workflow <<'EOF'
      - run: echo "${{ matrix.module }}"
EOF
expect_fail_naming "an undefined matrix property fails and is named" \
  ".github/workflows/ci.yml:7:" 'property "module" is not defined'

workflow <<'EOF'
      - run: echo $(ls)
EOF
expect_fail_naming "a shellcheck warning in a run script fails" ".github/workflows/ci.yml:7:" "SC2046"

workflow <<'EOF'
      - run: |
          echo start
          # shellcheck disable=SC2046 # each word is a separate argument on purpose
          echo $(ls)
EOF
expect_pass "a run-script exception with a reason that suppresses a finding passes"

workflow <<'EOF'
      - run: |
          echo start
          # shellcheck disable=SC2046
          echo $(ls)
EOF
expect_fail_naming "a run-script exception without a reason fails" "exception has no reason"

workflow <<'EOF'
      - run: |
          echo start
          # shellcheck disable=SC2046 # nothing left to excuse
          echo "clean"
EOF
expect_fail_naming "a run-script exception that suppresses nothing fails" "exception for SC2046 suppresses nothing"

workflow <<'EOF'
      - run: |
          echo start
          # shellcheck disable=SC2046 # words split on purpose
          case "$GITHUB_REF" in
            a) echo $(ls) ;;
            b) echo $(pwd) ;;
          esac
EOF
expect_fail_naming "a run-script exception over a case block fails" "exception covers"
