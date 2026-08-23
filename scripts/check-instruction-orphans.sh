#!/usr/bin/env bash
# Fails if a .md file under a definition root (a plugin's config/ directory,
# or the config-language conformance corpus) is not named by any
# instruction_file in that root. This is the reverse of the load-time check
# that an instruction_file naming no file is a load error
# (PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING): a sidecar nothing points at
# has no load error to surface it, so it silently rots instead.
#
# Scope: plugins/*/config (each plugin's own definition root) and
# testdata/config-language (the language conformance corpus, its own
# self-contained root for this purpose — a fixture's instruction_file never
# reaches outside it). README.md is docs, not a sidecar, and is excluded.
set -euo pipefail

root="${INSTRUCTION_ORPHAN_CHECK_ROOT:-$(pwd)}"
cd "$root"

fail=0

check_def_root() {
  local defroot="$1"
  [ -d "$defroot" ] || return 0

  local referenced
  referenced="$(mktemp)"
  trap 'rm -f "$referenced"' RETURN

  while IFS= read -r toml; do
    local dir
    dir="$(dirname "$toml")"
    while IFS= read -r rel; do
      [ -n "$rel" ] || continue
      realpath -m "$dir/$rel" >>"$referenced"
    done < <(grep -oE 'instruction_file[[:space:]]*=[[:space:]]*"[^"]*"' "$toml" | sed -E 's/.*"([^"]*)"$/\1/')
  done < <(find "$defroot" -name '*.toml')

  while IFS= read -r md; do
    [ "$(basename "$md")" = "README.md" ] && continue
    local abs
    abs="$(realpath -m "$md")"
    if ! grep -qxF "$abs" "$referenced" 2>/dev/null; then
      echo "orphan: $md (not named by any instruction_file under $defroot)"
      fail=1
    fi
  done < <(find "$defroot" -name '*.md')
}

while IFS= read -r cfg; do
  check_def_root "$cfg"
done < <(find plugins -maxdepth 2 -type d -name config 2>/dev/null | sort)

check_def_root "testdata/config-language"

if [ "$fail" -ne 0 ]; then
  echo
  echo "A Markdown file under a definition root is not referenced by any" >&2
  echo "instruction_file. Wire it in with instruction_file, or delete it if" >&2
  echo "it is stale." >&2
  exit 1
fi

echo "instruction-orphan check passed"
