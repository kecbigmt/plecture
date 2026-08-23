#!/usr/bin/env bash
# Proves check-instruction-orphans.sh actually catches an unreferenced
# sidecar before it is trusted as a CI gate: run it against a fixture tree
# with a deliberately orphaned .md file and require it to fail and name the
# file, then against a clean fixture tree and require it to pass.
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

# Dirty fixture: a .md sidecar no instruction_file names must be caught.
dirty=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean"' EXIT
mkdir -p "$dirty/plugins/acme/config/tasks"
cat >"$dirty/plugins/acme/config/tasks/work.toml" <<'EOF'
[work]
kind              = "task"
resource_observer = "issue"
instruction_file  = "work.md"
EOF
cat >"$dirty/plugins/acme/config/tasks/work.md" <<'EOF'
Resolve the issue.
EOF
cat >"$dirty/plugins/acme/config/tasks/orphan.md" <<'EOF'
Nothing points at this file.
EOF

if run_against "$dirty" >/tmp/instruction-orphan-selftest-dirty.log 2>&1; then
  echo "FAIL: checker passed against a fixture with an orphaned sidecar" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
if ! grep -q "orphan.md" /tmp/instruction-orphan-selftest-dirty.log; then
  echo "FAIL: checker did not name the orphaned file" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
if grep -q ": work.md" /tmp/instruction-orphan-selftest-dirty.log; then
  echo "FAIL: checker flagged the referenced sidecar as an orphan too" >&2
  cat /tmp/instruction-orphan-selftest-dirty.log >&2
  exit 1
fi
echo "ok: checker fails and names the file on a seeded orphaned sidecar"

# Clean fixture: every sidecar is named by an instruction_file, and README.md
# is excluded even though nothing references it.
clean=$(mktemp -d)
mkdir -p "$clean/plugins/acme/config/tasks"
cat >"$clean/plugins/acme/config/tasks/work.toml" <<'EOF'
[work]
kind              = "task"
resource_observer = "issue"
instruction_file  = "work.md"
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
