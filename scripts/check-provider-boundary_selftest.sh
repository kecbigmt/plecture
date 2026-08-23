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

# Every fixture var is declared (empty) before the single EXIT trap that
# cleans them all up, so a failure before a later fixture is created still
# cleans up the earlier ones without tripping "set -u" on an unset name —
# `rm -rf ""` is a harmless no-op under -f.
dirty="" dirty2="" dirty3="" dirty4="" dirty5="" dirty6="" clean=""
trap 'rm -rf "$dirty" "$dirty2" "$dirty3" "$dirty4" "$dirty5" "$dirty6" "$clean"' EXIT

run_against() {
  local fixture="$1"
  (
    cd "$fixture"
    "$checker"
  )
}

# Dirty fixture: a seeded provider token in app/ must be caught.
dirty=$(mktemp -d)
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

# Dirty fixture #5: a *_test.go file not on the allowlist must be scanned
# like any other file — test files are in scope by default; the allowlist
# is the one enumerated exception, not a directory-wide carve-out.
dirty5=$(mktemp -d)
mkdir -p "$dirty5/app/commands" "$dirty5/contracts"
cat > "$dirty5/app/commands/newthing_test.go" <<'EOF'
package commands

const seededTestViolation = "okf"
EOF

if run_against "$dirty5" >/tmp/boundary-selftest-dirty5.log 2>&1; then
  echo "FAIL: checker passed against a seeded violation in a non-allowlisted test file" >&2
  cat /tmp/boundary-selftest-dirty5.log >&2
  exit 1
fi
if ! grep -q "app/commands/newthing_test.go" /tmp/boundary-selftest-dirty5.log; then
  echo "FAIL: checker did not name the offending non-allowlisted test file" >&2
  cat /tmp/boundary-selftest-dirty5.log >&2
  exit 1
fi
echo "ok: checker fails and names the file for a seeded violation in a non-allowlisted test file"

# Dirty fixture #6: a test file whose path exactly matches an entry in
# check-provider-boundary-test-allowlist.txt must be skipped even though it
# has a seeded violation — the allowlist actually exempts what it names.
dirty6=$(mktemp -d)
# Read the first real entry directly, without piping into `head`: closing
# head's stdin after one line can send an earlier stage in the pipe a
# SIGPIPE, and under pipefail that intermittently fails this assignment.
allowlisted_path=""
while IFS= read -r line; do
  case "$line" in
    ''|'#'*) continue ;;
  esac
  allowlisted_path="$line"
  break
done < "$root/scripts/check-provider-boundary-test-allowlist.txt"
if [ -z "$allowlisted_path" ]; then
  echo "FAIL: check-provider-boundary-test-allowlist.txt has no entries to seed fixture #6 with" >&2
  exit 1
fi
mkdir -p "$dirty6/$(dirname "$allowlisted_path")" "$dirty6/contracts"
cat > "$dirty6/$allowlisted_path" <<'EOF'
package commands

const seededAllowlistedViolation = "okf"
EOF

if ! run_against "$dirty6" >/tmp/boundary-selftest-dirty6.log 2>&1; then
  echo "FAIL: checker flagged a seeded violation in an allowlisted file ($allowlisted_path)" >&2
  cat /tmp/boundary-selftest-dirty6.log >&2
  exit 1
fi
echo "ok: checker skips a seeded violation in an allowlisted file ($allowlisted_path)"

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
