#!/usr/bin/env bash
# Validates the shared agent-configuration invariants documented in
# CLAUDE.md's "Agent configuration" section:
#
#   - AGENTS.md is a symlink to CLAUDE.md, so a Claude session and a Codex
#     session read the same standing rules from one source of truth.
#   - Every directory under .claude/skills/ has a SKILL.md with both a
#     'name:' and a 'description:' frontmatter field, and a matching
#     relative symlink under .agents/skills/, so a Codex session sees the
#     same skill set as a Claude session.
#
# A repository with no skills yet (this repository's own state until its
# first skill is migrated or authored) passes: the skill loop below has
# nothing to iterate over.
set -euo pipefail

root="${AGENT_CONFIG_CHECK_ROOT:-$(pwd)}"
cd "$root"

fail=0

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
  local skill name skill_md expected target
  for skill in .claude/skills/*/; do
    skill="${skill%/}"
    name="$(basename "$skill")"
    skill_md="$skill/SKILL.md"

    if [ ! -f "$skill_md" ]; then
      echo "$skill: missing SKILL.md" >&2
      fail=1
      continue
    fi

    if ! grep -q '^name:' "$skill_md"; then
      echo "$skill_md: missing 'name:' frontmatter field" >&2
      fail=1
    fi
    if ! grep -q '^description:' "$skill_md"; then
      echo "$skill_md: missing 'description:' frontmatter field" >&2
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
