#!/usr/bin/env bash
# Proves check-instruction-orphans.sh actually catches an unreferenced
# sidecar before it is trusted as a CI gate: run it against fixture trees
# with a deliberately orphaned .md file, a commented-out reference that must
# not count, and a single-quoted reference that must, then against a clean
# fixture tree and require it to pass.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-instruction-orphans.sh"

run_against() {
  local fixture="$1"
  (
    cd "$fixture"
    INSTRUCTION_ORPHAN_CHECK_ROOT="$fixture" "$checker"
  )
}

# Dirty fixture: a .md sidecar no instructions element names must be caught,
# and a commented-out `file = "..."` line must not count as a reference.
dirty=$(mktemp -d)
trap 'rm -rf "$dirty" "$dirty2" "$clean"' EXIT
mkdir -p "$dirty/plugins/acme/config/tasks"
cat >"$dirty/plugins/acme/config/tasks/work.toml" <<'EOF'
[work]
kind              = "task"
resource_observer = "issue"
instructions      = [{ file = "work.md" }]
EOF
cat >"$dirty/plugins/acme/config/tasks/work.md" <<'EOF'
Resolve the issue.
EOF
cat >"$dirty/plugins/acme/config/tasks/orphan.md" <<'EOF'
Nothing points at this file.
EOF
cat >"$dirty/plugins/acme/config/tasks/commented-out.toml" <<'EOF'
[unrelated]
kind = "effect"
# instructions = [{ file = "orphan.md" }]
EOF

if run_against "$dirty" >/tmp/instruction-orphan-selftest-dirty.log 2>&1; then
  echo "FAIL: checker passed against a fixture with an orphaned sidecar" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
if ! grep -q "orphan.md" /tmp/instruction-orphan-selftest-dirty.log; then
  echo "FAIL: checker did not name the orphaned file (a commented-out reference may have wrongly counted)" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
if grep -q ": work.md" /tmp/instruction-orphan-selftest-dirty.log; then
  echo "FAIL: checker flagged the referenced sidecar as an orphan too" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded orphaned sidecar, ignoring a commented-out reference"

# Dirty fixture #2: a single-quoted `file = '...'` must still count as a
# reference — the checker must not report it as an orphan.
dirty2=$(mktemp -d)
mkdir -p "$dirty2/plugins/acme/config/tasks"
cat >"$dirty2/plugins/acme/config/tasks/work.toml" <<'EOF'
[work]
kind              = "task"
resource_observer = "issue"
instructions      = [{ file = 'work.md' }]
EOF
cat >"$dirty2/plugins/acme/config/tasks/work.md" <<'EOF'
Resolve the issue.
EOF

if ! run_against "$dirty2" >/tmp/instruction-orphan-selftest-dirty2.log 2>&1; then
  echo "FAIL: checker rejected a single-quoted file reference as an orphan" >&2
  cat /tmp/instruction-orphan-selftest-dirty2.log >&2
  exit 1
fi
echo "ok: checker recognizes a single-quoted file reference"

# Clean fixture: every sidecar is named by an instructions element, and
# README.md is excluded even though nothing references it.
clean=$(mktemp -d)
mkdir -p "$clean/plugins/acme/config/tasks"
cat >"$clean/plugins/acme/config/tasks/work.toml" <<'EOF'
[work]
kind              = "task"
resource_observer = "issue"
instructions      = [{ file = "work.md" }]
EOF
cat >"$clean/plugins/acme/config/tasks/work.md" <<'EOF'
Resolve the issue.
EOF
cat >"$clean/plugins/acme/config/README.md" <<'EOF'
Not a sidecar.
EOF

if ! run_against "$clean" >/tmp/instruction-orphan-selftest-clean.log 2>&1; then
  echo "FAIL: checker failed against a clean fixture" >&2
  cat /tmp/instruction-orphan-selftest-clean.log >&2
  exit 1
fi
echo "ok: checker passes on a clean fixture, excluding README.md"
