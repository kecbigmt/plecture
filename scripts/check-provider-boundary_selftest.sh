#!/usr/bin/env bash
# Proves check-provider-boundary.sh actually catches a violation before it is
# trusted as a CI gate: run it against a fixture tree with a deliberately
# seeded provider token and require it to fail and name the file, then run it
# against a clean fixture tree and require it to pass.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-provider-boundary.sh"

run_against() {
  local fixture="$1"
  (
    cd "$fixture"
    "$checker"
  )
}

# Dirty fixture: a seeded provider token in app/ must be caught.
dirty=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean"' EXIT
mkdir -p "$dirty/app/commands" "$dirty/contracts"
cat > "$dirty/app/commands/seeded.go" <<'EOF'
package commands

const seededViolation = "github"
EOF

if run_against "$dirty" >/tmp/boundary-selftest-dirty.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded provider token" >&2
  cat /tmp/boundary-selftest-dirty.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded.go" /tmp/boundary-selftest-dirty.log; then
  echo "FAIL: checker did not name the offending file" >&2
  cat /tmp/boundary-selftest-dirty.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded violation"

# Dirty fixture #2: a bare "owner/repo" placeholder (no digit suffix) must
# also be caught — this is the gap a reviewer found in an earlier revision.
dirty2=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean" "$dirty2"' EXIT
mkdir -p "$dirty2/app/commands" "$dirty2/contracts"
cat > "$dirty2/app/commands/seeded_ownerrepo.go" <<'EOF'
package commands

// Example: plecture cd owner/repo
const seededExample = "owner/repo"
EOF

if run_against "$dirty2" >/tmp/boundary-selftest-dirty2.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a bare owner/repo placeholder" >&2
  cat /tmp/boundary-selftest-dirty2.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_ownerrepo.go" /tmp/boundary-selftest-dirty2.log; then
  echo "FAIL: checker did not name the offending file for the owner/repo placeholder" >&2
  cat /tmp/boundary-selftest-dirty2.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a bare owner/repo placeholder"

# Clean fixture: no provider tokens, nothing to report.
clean=$(mktemp -d)
mkdir -p "$clean/app/commands" "$clean/contracts"
cat > "$clean/app/commands/clean.go" <<'EOF'
package commands

const notAViolation = "workspace"
EOF

if ! run_against "$clean" >/tmp/boundary-selftest-clean.log 2>&1; then
  echo "FAIL: checker failed against a clean fixture" >&2
  cat /tmp/boundary-selftest-clean.log >&2
  exit 1
fi
echo "ok: checker passes on a clean fixture"
