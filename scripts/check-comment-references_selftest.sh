#!/usr/bin/env bash
# Proves check-comment-references.sh actually catches a violation before it
# is trusted as a CI gate: run it against fixture trees with deliberately
# seeded offending comments and require it to fail and name the file, then
# run it against a clean fixture tree and require it to pass.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-comment-references.sh"

run_against() {
  local fixture="$1"
  (
    cd "$fixture"
    COMMENT_REF_CHECK_ROOT="$fixture" "$checker"
  )
}

# Dirty fixture: a seeded "#<digits>" issue reference must be caught.
dirty=$(mktemp -d)
trap 'rm -rf "$dirty" "$dirty2" "$dirty3" "$dirty4" "$clean"' EXIT
mkdir -p "$dirty/app/commands"
cat > "$dirty/app/commands/seeded.go" <<'EOF'
package commands

// Fixes the race described in #39.
const seededViolation = "value"
EOF

if run_against "$dirty" >/tmp/comment-ref-selftest-dirty.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded issue reference" >&2
  cat /tmp/comment-ref-selftest-dirty.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded.go" /tmp/comment-ref-selftest-dirty.log; then
  echo "FAIL: checker did not name the offending file" >&2
  cat /tmp/comment-ref-selftest-dirty.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded issue reference"

# Dirty fixture #2: a seeded "ADR-<digits>" reference must also be caught.
dirty2=$(mktemp -d)
mkdir -p "$dirty2/app/commands"
cat > "$dirty2/app/commands/seeded_adr.go" <<'EOF'
package commands

// Follows the dynamic-instantiation shape (ADR-003).
const seededAdr = "value"
EOF

if run_against "$dirty2" >/tmp/comment-ref-selftest-dirty2.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded ADR reference" >&2
  cat /tmp/comment-ref-selftest-dirty2.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_adr.go" /tmp/comment-ref-selftest-dirty2.log; then
  echo "FAIL: checker did not name the offending file for the ADR reference" >&2
  cat /tmp/comment-ref-selftest-dirty2.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded ADR reference"

# Dirty fixture #3: a bare "#<digits>" glued after other punctuation (not a
# word char) inside a comment must be caught even when a task-instance-id
# example appears on the same line.
dirty3=$(mktemp -d)
mkdir -p "$dirty3/app/commands"
cat > "$dirty3/app/commands/seeded_mixed.go" <<'EOF'
package commands

// Same shape as review#1, tracked in issue #39.
const seededMixed = "value"
EOF

if run_against "$dirty3" >/tmp/comment-ref-selftest-dirty3.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a bare issue reference" >&2
  cat /tmp/comment-ref-selftest-dirty3.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_mixed.go" /tmp/comment-ref-selftest-dirty3.log; then
  echo "FAIL: checker did not name the offending file for the mixed-line case" >&2
  cat /tmp/comment-ref-selftest-dirty3.log >&2
  exit 1
fi
echo "ok: checker fails on a bare issue reference even beside an instance-id example"

# Dirty fixture #4: a seeded reference inside a *_test.go file must be caught
# too — the standing rule applies to all Go code comments, tests included.
dirty4=$(mktemp -d)
mkdir -p "$dirty4/app/commands"
cat > "$dirty4/app/commands/seeded_test.go" <<'EOF'
package commands

// Regression test for #39.
func TestSeededViolation(t *testing.T) {}
EOF

if run_against "$dirty4" >/tmp/comment-ref-selftest-dirty4.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded reference in a _test.go file" >&2
  cat /tmp/comment-ref-selftest-dirty4.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_test.go" /tmp/comment-ref-selftest-dirty4.log; then
  echo "FAIL: checker did not name the offending _test.go file" >&2
  cat /tmp/comment-ref-selftest-dirty4.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded reference in a _test.go file"

# Clean fixture: task-instance-id examples, string literals, and an
# allowlisted line must all pass without report.
clean=$(mktemp -d)
mkdir -p "$clean/app/commands"
cat > "$clean/app/commands/clean.go" <<'EOF'
package commands

// InstanceKey derives the numbered form of a dynamic task instance:
// "<taskID>#<instanceID>", such as review#1 or review#2.
const helpText = "cleanup review#1, e.g. https://example.com/path#39"

// Tracks the fixture case from #39. comment-ref-allow: fixture exercising the allowlist escape hatch
const allowlisted = "value"
EOF

if ! run_against "$clean" >/tmp/comment-ref-selftest-clean.log 2>&1; then
  echo "FAIL: checker failed against a clean fixture" >&2
  cat /tmp/comment-ref-selftest-clean.log >&2
  exit 1
fi
echo "ok: checker passes on a clean fixture"
