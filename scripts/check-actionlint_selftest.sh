#!/usr/bin/env bash
# Proves check-actionlint.sh against seeded defects in a fixture repository:
# an expression naming an undefined property fails and is named, and so do
# a shellcheck warning and a suppression directive inside a `run:` script.
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
expect_fail_naming "a suppression directive in a run script fails" \
  ".github/workflows/ci.yml:7:" "suppression directive" "This check takes no exception"

workflow <<'EOF'
      - run: |
          echo start; echo "# shellcheck disable=SC1000"; # shellcheck disable=SC2046
          echo $(ls)
EOF
expect_fail_naming "a run-script directive after a quoted look-alike is still found" "suppression directive"

workflow <<'EOF'
      - run: |
          echo start
          # shellcheck source=disable=foo disable=SC2034
          unused=1
EOF
expect_fail_naming "a run-script disable key after a look-alike in another value is still found" "suppression directive"

workflow <<'EOF'
      - run: echo "${{ matrix.module }}"
EOF
printf 'paths:\n  .github/workflows/ci.yml:\n    ignore:\n      - .*\n' > "$fixture/.github/actionlint.yaml"
expect_fail_naming "an actionlint configuration file fails" ".github/actionlint.yaml:1: actionlint configuration file"
