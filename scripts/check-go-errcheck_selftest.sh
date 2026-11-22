#!/usr/bin/env bash
# Proves check-go-errcheck.sh against seeded defects in a fixture workspace:
# a discarded error that predates the base passes, one a commit adds fails
# and is named, and a directive that would hide one fails and is named.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-go-errcheck.sh"
go_version="$(go env GOVERSION | sed 's/^go//')"

fixture="$(mktemp -d)"
log="$fixture.log"
trap 'rm -rf "$fixture" "$log"' EXIT

mkdir -p "$fixture/mod"
cat > "$fixture/go.work" <<EOF
go $go_version

use ./mod
EOF
cat > "$fixture/mod/go.mod" <<EOF
module example.com/mod

go $go_version
EOF
cat > "$fixture/mod/old.go" <<'EOF'
package main

import "os"

func main() {
	os.Remove("predates-the-base")
}
EOF

git_fixture() {
  git -C "$fixture" -c user.email=test@example.com -c user.name=test "$@"
}
git_fixture init -q
git_fixture add -A
git_fixture commit -q -m base
base="$(git_fixture rev-parse HEAD)"

# commit_new writes mod/new.go with the given body and commits it on top of
# the base, so the check sees it as an added line.
commit_new() {
  git_fixture reset -q --hard "$base"
  cat > "$fixture/mod/new.go"
  git_fixture add -A
  git_fixture commit -q -m change
}

run_check() {
  GO_ERRCHECK_CHECK_ROOT="$fixture" "$checker" "$base" > "$log" 2>&1
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

commit_new <<'EOF'
package main

import "os"

func cleanup() error { return os.Remove("checked") }
EOF
expect_pass "a change adding no discarded error passes while an older site remains"

commit_new <<'EOF'
package main

import "os"

func cleanup() { _ = os.Remove("discarded") }
EOF
expect_fail_naming "a change adding a blank-assigned error fails and names the line" "mod/new.go:5:" "(errcheck)"

commit_new <<'EOF'
package main

import "os"

func cleanup() {
	os.Remove("unchecked")
}
EOF
expect_fail_naming "a change adding an unchecked call fails and names the line" "mod/new.go:6:" "(errcheck)"

commit_new <<'EOF'
package main

import "os"

func cleanup() {
	os.Remove("best-effort") //nolint:errcheck // the file may already be gone
}
EOF
expect_fail_naming "a change adding a nolint over a discarded error fails and names the line" \
  "mod/new.go:6: suppression directive" "This check takes no exception"

# expect_line_starting asserts a finding line starts with exactly this path,
# so a doubled or missing module prefix is caught.
expect_line_starting() {
  local name="$1" want="$2"
  if run_check; then
    echo "FAIL: $name: checker passed" >&2
    cat "$log" >&2
    exit 1
  fi
  if ! awk -v w="$want" 'index($0, w) == 1 { found = 1 } END { exit !found }' "$log"; then
    echo "FAIL: $name: no line starts with '$want'" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $name"
}

git_fixture reset -q --hard "$base"
mkdir -p "$fixture/nested/mod2"
cat > "$fixture/nested/mod2/go.mod" <<EOF
module example.com/mod2

go $go_version
EOF
cat > "$fixture/go.work" <<EOF
go $go_version

use (
	./mod
	./nested/mod2
)
EOF
cat > "$fixture/nested/mod2/new.go" <<'EOF'
package mod2

import "os"

func Cleanup() { os.Remove("nested") }
EOF
git_fixture add -A
git_fixture commit -q -m nested
expect_line_starting "a finding in a nested module names its path from the repository root" "nested/mod2/new.go:5:"

commit_new <<'EOF'
//go:build !browser

package main

import "os"

func cleanup() {
	os.Remove("default-only")
}
EOF
expect_fail_naming "a change adding a discarded error in a file the browser tag excludes fails" "mod/new.go:8:"
