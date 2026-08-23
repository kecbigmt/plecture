#!/usr/bin/env bash
# Proves the check-instruction-orphans.sh wrapper actually locates roots and
# runs the real checker end to end. The checker's own parsing behavior
# (comments, quote styles, README exclusion, ...) is covered by
# app/internal/instructionorphans's own Go tests — `go test ./internal/...`
# from `app/` — not duplicated here.
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

dirty=$(mktemp -d)
trap 'rm -rf "$dirty" "$clean"' EXIT
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
echo "ok: the wrapper locates plugins/*/config and fails, naming the orphan"

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
echo "ok: the wrapper passes on a clean fixture"
