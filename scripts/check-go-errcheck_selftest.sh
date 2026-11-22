#!/usr/bin/env bash
# Proves check-go-errcheck.sh against seeded defects in a fixture workspace:
# a discarded error that predates the base passes, one a commit adds fails
# and is named, and an exception passes only with a reason and only while it
# still suppresses something.
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
expect_pass "an exception with a reason that suppresses a finding passes"

commit_new <<'EOF'
package main

import "os"

func cleanup() {
	os.Remove("no-reason") //nolint:errcheck
}
EOF
expect_fail_naming "an exception without a reason fails" "mod/new.go:6:" "(nolintlint)"

commit_new <<'EOF'
package main

func cleanup() {
	println("nothing to excuse") //nolint:errcheck // stale
}
EOF
expect_fail_naming "an exception that suppresses nothing fails" "mod/new.go:4:" "(nolintlint)"
