#!/usr/bin/env bash
# Validates the shared agent-configuration invariants documented in
# CLAUDE.md's "Agent configuration" section:
#
#   - AGENTS.md is a symlink to CLAUDE.md, so a Claude session and a Codex
#     session read the same standing rules from one source of truth.
#   - Every directory under .claude/skills/ has a SKILL.md whose YAML
#     frontmatter block (the lines between the first '---' and the next
#     '---') has both a 'name:' and a 'description:' field, and a matching
#     relative symlink under .agents/skills/, so a Codex session sees the
#     same skill set as a Claude session.
#   - Every symlink under .agents/skills/ has a matching directory under
#     .claude/skills/ — an entry with no match is an orphan left behind by
#     a deleted or renamed skill, and breaks the "same skill set" guarantee
#     in the other direction.
#
# A repository with no skills yet (this repository's own state until its
# first skill is migrated or authored) passes: the skill loops below have
# nothing to iterate over.
set -euo pipefail

root="${AGENT_CONFIG_CHECK_ROOT:-$(pwd)}"
cd "$root"

fail=0

# Prints the lines strictly between the first '---' line and the next
# '---' line of a SKILL.md, i.e. its YAML frontmatter block. Prints
# nothing if the file does not open with '---' on its first line, or if
# the block is never closed with a second '---' before EOF -- an
# unterminated block is not valid frontmatter, so its 'name:'/
# 'description:' lines must not count.
extract_frontmatter() {
  awk '
    NR == 1 && $0 == "---" { infm = 1; next }
    infm && $0 == "---" { closed = 1; exit }
    infm { buf = buf $0 "\n" }
    END { if (closed) printf "%s", buf }
  ' "$1"
}

check_rule_file_symlink() {
  if [ ! -L AGENTS.md ]; then
    echo "AGENTS.md is not a symlink (expected -> CLAUDE.md)" >&2
    fail=1
    return
  fi
  local target
  target="$(readlink AGENTS.md)"
  if [ "$target" != "CLAUDE.md" ]; then
    echo "AGENTS.md symlink points to '$target', expected 'CLAUDE.md'" >&2
    fail=1
  fi
}

check_skills() {
  shopt -s nullglob
  local skill name skill_md expected target frontmatter

  # Forward: every .claude/skills/<name> needs a SKILL.md with both
  # frontmatter fields, and a correctly-pointing .agents/skills/<name>
  # symlink.
  for skill in .claude/skills/*/; do
    skill="${skill%/}"
    name="$(basename "$skill")"
    skill_md="$skill/SKILL.md"

    if [ ! -f "$skill_md" ]; then
      echo "$skill: missing SKILL.md" >&2
      fail=1
      continue
    fi

    frontmatter="$(extract_frontmatter "$skill_md")"
    if ! printf '%s\n' "$frontmatter" | grep -q '^name:'; then
      echo "$skill_md: missing 'name:' field in its frontmatter block" >&2
      fail=1
    fi
    if ! printf '%s\n' "$frontmatter" | grep -q '^description:'; then
      echo "$skill_md: missing 'description:' field in its frontmatter block" >&2
      fail=1
    fi

    expected="../../.claude/skills/$name"
    if [ ! -L ".agents/skills/$name" ]; then
      echo ".agents/skills/$name is not a symlink (expected -> $expected)" >&2
      fail=1
      continue
    fi
    target="$(readlink ".agents/skills/$name")"
    if [ "$target" != "$expected" ]; then
      echo ".agents/skills/$name symlink points to '$target', expected '$expected'" >&2
      fail=1
    fi
  done

  # Reverse: every .agents/skills/<name> symlink needs a matching
  # .claude/skills/<name> directory. An entry that fails this is an orphan
  # left behind by a deleted or renamed skill.
  for entry in .agents/skills/*; do
    [ -L "$entry" ] || continue
    name="$(basename "$entry")"
    if [ ! -d ".claude/skills/$name" ]; then
      echo "$entry has no matching .claude/skills/$name directory (orphan)" >&2
      fail=1
    fi
  done

  shopt -u nullglob
}

check_rule_file_symlink
check_skills

if [ "$fail" -ne 0 ]; then
  echo >&2
  echo "Agent configuration invariant violated. See CLAUDE.md's Agent" >&2
  echo "configuration section for the symlink and frontmatter conventions." >&2
  exit 1
fi

echo "agent-config check passed"
