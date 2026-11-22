#!/usr/bin/env bash
# Proves check-go-unused.sh against seeded defects in a fixture workspace:
# a dead function fails and is named, an identifier only a tagged file uses
# passes, and any suppression directive fails and is named.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-go-unused.sh"
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
cat > "$fixture/mod/main.go" <<'EOF'
package main

func main() {}
EOF
git -C "$fixture" init -q

run_check() {
  git -C "$fixture" add -A
  GO_UNUSED_CHECK_ROOT="$fixture" "$checker" > "$log" 2>&1
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

expect_pass "a clean module passes"

cat > "$fixture/mod/dead.go" <<'EOF'
package main

func dead() {}
EOF
expect_fail_naming "a dead function fails and is named" "mod/dead.go:3:6: U1000" "This check takes no exception"

cat > "$fixture/mod/dead.go" <<'EOF'
package main

func onlyIntegrationUses() {}
EOF
cat > "$fixture/mod/main_integration_test.go" <<'EOF'
//go:build integration

package main

import "testing"

func TestUses(t *testing.T) { onlyIntegrationUses() }
EOF
expect_pass "an identifier only an integration-tagged file uses passes"

rm "$fixture/mod/main_integration_test.go"
cat > "$fixture/mod/dead.go" <<'EOF'
package main

//lint:ignore U1000 kept for later
func onlyIntegrationUses() {}
EOF
expect_fail_naming "a staticcheck ignore over dead code fails" "mod/dead.go:3: suppression directive"

cat > "$fixture/mod/dead.go" <<'EOF'
package main

import "os"

// /nolint:errcheck
func cleanup() {
	os.Remove("one")
	os.Remove("two")
}

func init() { cleanup() }
EOF
expect_fail_naming "a slash-prefixed nolint over a function fails" "mod/dead.go:5: suppression directive"

cat > "$fixture/mod/dead.go" <<'EOF'
package main

import "os"

func cleanup() {
	os.Remove("one") ///nolint:errcheck
}

func init() { cleanup() }
EOF
expect_fail_naming "a trailing slash-prefixed nolint fails" "mod/dead.go:6: suppression directive"
