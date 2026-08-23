#!/usr/bin/env bash
# Finds a Markdown file under a definition root (a plugin's config/
# directory, or the config-language conformance corpus) that no task's
# `instructions` element names by its `file`. This is the reverse of the
# load-time check that such a file naming no file is a load error
# (PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING): a sidecar nothing points at
# has no load error to surface it, so it silently rots instead.
#
# The actual check is app/internal/instructionorphans, invoked here through
# app/cmd/check-instruction-orphans, because the invariant it guards —
# "every sidecar is referenced" — is a claim about decoded TOML values, not
# about source text: a shell text scan gets a commented-out reference and an
# unusual (but valid) quote spelling wrong in opposite directions.
set -euo pipefail

root="${INSTRUCTION_ORPHAN_CHECK_ROOT:-$(pwd)}"
app_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../app" && pwd)"
cd "$root"

roots=()
while IFS= read -r cfg; do
  roots+=("$(cd "$cfg" && pwd)")
done < <(find plugins -maxdepth 2 -type d -name config 2>/dev/null | sort)
if [ -d testdata/config-language ]; then
  roots+=("$(cd testdata/config-language && pwd)")
fi

if [ ${#roots[@]} -eq 0 ]; then
  echo "instruction-orphan check passed (no definition roots found)"
  exit 0
fi

(cd "$app_dir" && go run ./cmd/check-instruction-orphans "${roots[@]}")
