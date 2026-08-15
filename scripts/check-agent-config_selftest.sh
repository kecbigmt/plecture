#!/usr/bin/env bash
# Proves check-agent-config.sh actually catches a violation before it is
# trusted as a CI gate: run it against fixture trees with deliberately
# seeded invariant breaks and require it to fail and name the offending
# path, then run it against clean fixtures (including a zero-skill tree,
# matching this repository's own state) and require it to pass.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-agent-config.sh"

run_against() {
  local fixture="$1"
  (
    cd "$fixture"
    AGENT_CONFIG_CHECK_ROOT="$fixture" "$checker"
  )
}

# A fixture with a properly configured skill, reused as the base for each
# dirty variant below so only the one invariant under test is broken.
make_clean_fixture() {
  local fixture="$1"
  mkdir -p "$fixture/.claude/skills/demo" "$fixture/.agents/skills"
  cat > "$fixture/.claude/skills/demo/SKILL.md" <<'EOF'
---
name: demo
description: A fixture skill used to exercise the agent-config checker.
---

# Demo
EOF
  ln -s "../../.claude/skills/demo" "$fixture/.agents/skills/demo"
  printf 'placeholder\n' > "$fixture/CLAUDE.md"
  ln -s "CLAUDE.md" "$fixture/AGENTS.md"
}

fixtures=()
cleanup() {
  local f
  for f in "${fixtures[@]}"; do
    rm -rf "$f"
  done
}
trap cleanup EXIT

new_fixture() {
  local f
  f="$(mktemp -d)"
  fixtures+=("$f")
  echo "$f"
}

# Dirty fixture: AGENTS.md is a plain file, not a symlink.
dirty_not_symlink="$(new_fixture)"
make_clean_fixture "$dirty_not_symlink"
rm "$dirty_not_symlink/AGENTS.md"
printf 'placeholder\n' > "$dirty_not_symlink/AGENTS.md"

if run_against "$dirty_not_symlink" >/tmp/agent-config-selftest-not-symlink.log 2>&1; then
  echo "FAIL: checker passed although AGENTS.md is not a symlink" >&2
  cat /tmp/agent-config-selftest-not-symlink.log >&2
  exit 1
fi
if ! grep -q "AGENTS.md" /tmp/agent-config-selftest-not-symlink.log; then
  echo "FAIL: checker did not name AGENTS.md" >&2
  cat /tmp/agent-config-selftest-not-symlink.log >&2
  exit 1
fi
echo "ok: checker fails when AGENTS.md is not a symlink"

# Dirty fixture: AGENTS.md symlinks to the wrong target.
dirty_wrong_target="$(new_fixture)"
make_clean_fixture "$dirty_wrong_target"
rm "$dirty_wrong_target/AGENTS.md"
printf 'placeholder\n' > "$dirty_wrong_target/OTHER.md"
ln -s "OTHER.md" "$dirty_wrong_target/AGENTS.md"

if run_against "$dirty_wrong_target" >/tmp/agent-config-selftest-wrong-target.log 2>&1; then
  echo "FAIL: checker passed although AGENTS.md points at the wrong file" >&2
  cat /tmp/agent-config-selftest-wrong-target.log >&2
  exit 1
fi
if ! grep -q "AGENTS.md" /tmp/agent-config-selftest-wrong-target.log; then
  echo "FAIL: checker did not name AGENTS.md" >&2
  cat /tmp/agent-config-selftest-wrong-target.log >&2
  exit 1
fi
echo "ok: checker fails when AGENTS.md points at the wrong target"

# Dirty fixture: a skill directory has no SKILL.md.
dirty_missing_skill_md="$(new_fixture)"
make_clean_fixture "$dirty_missing_skill_md"
rm "$dirty_missing_skill_md/.claude/skills/demo/SKILL.md"

if run_against "$dirty_missing_skill_md" >/tmp/agent-config-selftest-missing-skill-md.log 2>&1; then
  echo "FAIL: checker passed although demo/SKILL.md is missing" >&2
  cat /tmp/agent-config-selftest-missing-skill-md.log >&2
  exit 1
fi
if ! grep -q ".claude/skills/demo" /tmp/agent-config-selftest-missing-skill-md.log; then
  echo "FAIL: checker did not name the skill directory" >&2
  cat /tmp/agent-config-selftest-missing-skill-md.log >&2
  exit 1
fi
echo "ok: checker fails when a skill directory has no SKILL.md"

# Dirty fixture: SKILL.md is missing the 'name:' frontmatter field.
dirty_missing_name="$(new_fixture)"
make_clean_fixture "$dirty_missing_name"
cat > "$dirty_missing_name/.claude/skills/demo/SKILL.md" <<'EOF'
---
description: A fixture skill used to exercise the agent-config checker.
---

# Demo
EOF

if run_against "$dirty_missing_name" >/tmp/agent-config-selftest-missing-name.log 2>&1; then
  echo "FAIL: checker passed although SKILL.md has no 'name:' field" >&2
  cat /tmp/agent-config-selftest-missing-name.log >&2
  exit 1
fi
if ! grep -q "name:" /tmp/agent-config-selftest-missing-name.log; then
  echo "FAIL: checker did not mention the missing 'name:' field" >&2
  cat /tmp/agent-config-selftest-missing-name.log >&2
  exit 1
fi
echo "ok: checker fails when SKILL.md has no 'name:' frontmatter field"

# Dirty fixture: SKILL.md is missing the 'description:' frontmatter field.
dirty_missing_description="$(new_fixture)"
make_clean_fixture "$dirty_missing_description"
cat > "$dirty_missing_description/.claude/skills/demo/SKILL.md" <<'EOF'
---
name: demo
---

# Demo
EOF

if run_against "$dirty_missing_description" >/tmp/agent-config-selftest-missing-description.log 2>&1; then
  echo "FAIL: checker passed although SKILL.md has no 'description:' field" >&2
  cat /tmp/agent-config-selftest-missing-description.log >&2
  exit 1
fi
if ! grep -q "description:" /tmp/agent-config-selftest-missing-description.log; then
  echo "FAIL: checker did not mention the missing 'description:' field" >&2
  cat /tmp/agent-config-selftest-missing-description.log >&2
  exit 1
fi
echo "ok: checker fails when SKILL.md has no 'description:' frontmatter field"

# Dirty fixture: .agents/skills/demo does not exist.
dirty_missing_agents_symlink="$(new_fixture)"
make_clean_fixture "$dirty_missing_agents_symlink"
rm "$dirty_missing_agents_symlink/.agents/skills/demo"

if run_against "$dirty_missing_agents_symlink" >/tmp/agent-config-selftest-missing-agents-symlink.log 2>&1; then
  echo "FAIL: checker passed although .agents/skills/demo is missing" >&2
  cat /tmp/agent-config-selftest-missing-agents-symlink.log >&2
  exit 1
fi
if ! grep -q ".agents/skills/demo" /tmp/agent-config-selftest-missing-agents-symlink.log; then
  echo "FAIL: checker did not name .agents/skills/demo" >&2
  cat /tmp/agent-config-selftest-missing-agents-symlink.log >&2
  exit 1
fi
echo "ok: checker fails when .agents/skills/demo is missing"

# Dirty fixture: .agents/skills/demo exists but is a plain file, not a symlink.
dirty_agents_not_symlink="$(new_fixture)"
make_clean_fixture "$dirty_agents_not_symlink"
rm "$dirty_agents_not_symlink/.agents/skills/demo"
printf 'not a symlink\n' > "$dirty_agents_not_symlink/.agents/skills/demo"

if run_against "$dirty_agents_not_symlink" >/tmp/agent-config-selftest-agents-not-symlink.log 2>&1; then
  echo "FAIL: checker passed although .agents/skills/demo is not a symlink" >&2
  cat /tmp/agent-config-selftest-agents-not-symlink.log >&2
  exit 1
fi
if ! grep -q ".agents/skills/demo" /tmp/agent-config-selftest-agents-not-symlink.log; then
  echo "FAIL: checker did not name .agents/skills/demo" >&2
  cat /tmp/agent-config-selftest-agents-not-symlink.log >&2
  exit 1
fi
echo "ok: checker fails when .agents/skills/demo is not a symlink"

# Dirty fixture: .agents/skills/demo symlinks to the wrong target.
dirty_agents_wrong_target="$(new_fixture)"
make_clean_fixture "$dirty_agents_wrong_target"
rm "$dirty_agents_wrong_target/.agents/skills/demo"
ln -s "../../.claude/skills/other" "$dirty_agents_wrong_target/.agents/skills/demo"

if run_against "$dirty_agents_wrong_target" >/tmp/agent-config-selftest-agents-wrong-target.log 2>&1; then
  echo "FAIL: checker passed although .agents/skills/demo points at the wrong target" >&2
  cat /tmp/agent-config-selftest-agents-wrong-target.log >&2
  exit 1
fi
if ! grep -q ".agents/skills/demo" /tmp/agent-config-selftest-agents-wrong-target.log; then
  echo "FAIL: checker did not name .agents/skills/demo" >&2
  cat /tmp/agent-config-selftest-agents-wrong-target.log >&2
  exit 1
fi
echo "ok: checker fails when .agents/skills/demo points at the wrong target"

# Dirty fixture: .agents/skills has an orphan entry with no matching
# .claude/skills directory (e.g. left behind by a deleted or renamed
# skill).
dirty_orphan_agents_entry="$(new_fixture)"
make_clean_fixture "$dirty_orphan_agents_entry"
ln -s "../../.claude/skills/ghost" "$dirty_orphan_agents_entry/.agents/skills/ghost"

if run_against "$dirty_orphan_agents_entry" >/tmp/agent-config-selftest-orphan.log 2>&1; then
  echo "FAIL: checker passed although .agents/skills/ghost is an orphan" >&2
  cat /tmp/agent-config-selftest-orphan.log >&2
  exit 1
fi
if ! grep -q ".agents/skills/ghost" /tmp/agent-config-selftest-orphan.log; then
  echo "FAIL: checker did not name the orphan .agents/skills/ghost entry" >&2
  cat /tmp/agent-config-selftest-orphan.log >&2
  exit 1
fi
echo "ok: checker fails when .agents/skills has an orphan entry"

# Dirty fixture: 'name:'/'description:' appear in the SKILL.md body, not in
# the frontmatter block, so an empty frontmatter must still be rejected.
dirty_fields_outside_frontmatter="$(new_fixture)"
make_clean_fixture "$dirty_fields_outside_frontmatter"
cat > "$dirty_fields_outside_frontmatter/.claude/skills/demo/SKILL.md" <<'EOF'
---
---

# Demo

name: demo
description: A fixture skill used to exercise the agent-config checker.
EOF

if run_against "$dirty_fields_outside_frontmatter" >/tmp/agent-config-selftest-fields-outside.log 2>&1; then
  echo "FAIL: checker passed although 'name:'/'description:' are outside the frontmatter block" >&2
  cat /tmp/agent-config-selftest-fields-outside.log >&2
  exit 1
fi
if ! grep -q "frontmatter" /tmp/agent-config-selftest-fields-outside.log; then
  echo "FAIL: checker did not mention frontmatter" >&2
  cat /tmp/agent-config-selftest-fields-outside.log >&2
  exit 1
fi
echo "ok: checker fails when 'name:'/'description:' are outside the frontmatter block"

# Clean fixture: a properly configured skill must pass without report.
clean="$(new_fixture)"
make_clean_fixture "$clean"

if ! run_against "$clean" >/tmp/agent-config-selftest-clean.log 2>&1; then
  echo "FAIL: checker failed against a clean fixture" >&2
  cat /tmp/agent-config-selftest-clean.log >&2
  exit 1
fi
echo "ok: checker passes on a clean fixture with one properly configured skill"

# Clean fixture: zero skills must also pass, matching this repository's own
# state until its first skill is migrated or authored.
clean_empty="$(new_fixture)"
mkdir -p "$clean_empty/.claude/skills" "$clean_empty/.agents/skills"
printf 'placeholder\n' > "$clean_empty/CLAUDE.md"
ln -s "CLAUDE.md" "$clean_empty/AGENTS.md"

if ! run_against "$clean_empty" >/tmp/agent-config-selftest-clean-empty.log 2>&1; then
  echo "FAIL: checker failed against a fixture with zero skills" >&2
  cat /tmp/agent-config-selftest-clean-empty.log >&2
  exit 1
fi
echo "ok: checker passes on a clean fixture with zero skills"
