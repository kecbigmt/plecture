#!/usr/bin/env bash
# Proves check-provider-boundary.sh actually catches a violation before it is
# trusted as a CI gate: run it against a fixture tree with a deliberately
# seeded provider token and require it to fail and name the file, then run it
# against a clean fixture tree and require it to pass.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-provider-boundary.sh"

# The vocabulary is derived from the real repo's plugins/ (see the checker's
# own comment on why $root/plugins, not the fixture, is the source), so a
# name shipped by any real plugin works as a seed here. "okf" is not GitHub
# vocabulary and was invisible to the checker before vocabulary derivation
# replaced the hand-kept github-only list — seeding it proves the derivation
# actually works, not just that the pre-existing hand-kept patterns still do.

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

// Example: plect cd owner/repo
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

# Dirty fixture #3: a shipped plugin name outside the old github-only
# vocabulary must also be caught. The hand-kept pattern covered only
# github/gh/pvti/owner-repo, so claude, codex, slack, tmux, and okf leakage
# was invisible.
dirty3=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean" "$dirty2" "$dirty3"' EXIT
mkdir -p "$dirty3/app/commands" "$dirty3/contracts"
cat > "$dirty3/app/commands/seeded_vocab.go" <<'EOF'
package commands

const seededPluginName = "okf"
EOF

if run_against "$dirty3" >/tmp/boundary-selftest-dirty3.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded non-github plugin name" >&2
  cat /tmp/boundary-selftest-dirty3.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_vocab.go" /tmp/boundary-selftest-dirty3.log; then
  echo "FAIL: checker did not name the offending file for the non-github plugin name" >&2
  cat /tmp/boundary-selftest-dirty3.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a derived (non-github) plugin name"

# Dirty fixture #4: a dotted event-type-style token (a plugin name
# immediately followed by "." and more text, e.g. an event Type prefix)
# must also be caught. An earlier boundary excluded "." on both sides of a
# vocabulary word to avoid matching inside a Go import path, which also
# made it blind to exactly this shape.
dirty4=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean" "$dirty2" "$dirty3" "$dirty4"' EXIT
mkdir -p "$dirty4/app/commands" "$dirty4/contracts"
cat > "$dirty4/app/commands/seeded_dotted.go" <<'EOF'
package commands

const seededDottedType = "slack.message"
EOF

if run_against "$dirty4" >/tmp/boundary-selftest-dirty4.log 2>&1; then
  echo "FAIL: checker passed against a fixture with a seeded dotted provider token" >&2
  cat /tmp/boundary-selftest-dirty4.log >&2
  exit 1
fi
if ! grep -q "app/commands/seeded_dotted.go" /tmp/boundary-selftest-dirty4.log; then
  echo "FAIL: checker did not name the offending file for the dotted provider token" >&2
  cat /tmp/boundary-selftest-dirty4.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a dotted provider token"

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
