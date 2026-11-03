#!/usr/bin/env bash
# Proves check-adr-lifecycle.sh catches each violation it exists for, and
# accepts the legitimate shapes, before it is trusted as a CI gate. Each case
# builds a small git fixture so the diff-scoped immutability check sees real
# base and head commits.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-adr-lifecycle.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

old=2026-01-01-old-decision
new=2026-11-05-new-decision

commit_all() {
  git -C "$1" add -A
  git -C "$1" -c user.email=test@example.com -c user.name=test commit -q -m "$2"
}

# new_fixture <name>: an empty git repo holding docs/adr/, echoed as a path.
new_fixture() {
  local dir="$work/$1"
  mkdir -p "$dir/docs/adr"
  git -C "$dir" init -q
  echo "$dir"
}

write_old_plain() {
  cat >"$1/docs/adr/$old.md" <<EOF
# Old decision

## Context

Original context.
EOF
}

write_old_linked() {
  cat >"$1/docs/adr/$old.md" <<EOF
---
superseded_by: $new
---

# Old decision

## Context

Original context.
EOF
}

write_new_linked() {
  cat >"$1/docs/adr/$new.md" <<EOF
---
supersedes: $old
---

# New decision

## Context

Why.

## Supersession scope

Replaces the original context.

## Decision

What.
EOF
}

# expect <name> <pass|fail> <needle> <args...>: runs the checker in fixture
# <name>; on fail the output must contain <needle>.
expect() {
  local name="$1" verdict="$2" needle="$3"
  shift 3
  local log="$work/$name.log" status=0
  ADR_LIFECYCLE_CHECK_ROOT="$work/$name" "$checker" "$@" >"$log" 2>&1 || status=$?
  if [ "$verdict" = pass ] && [ "$status" -ne 0 ]; then
    echo "FAIL: $name: checker failed, expected pass" >&2
    cat "$log" >&2
    exit 1
  fi
  if [ "$verdict" = fail ]; then
    if [ "$status" -eq 0 ]; then
      echo "FAIL: $name: checker passed, expected failure" >&2
      cat "$log" >&2
      exit 1
    fi
    if ! grep -qF -- "$needle" "$log"; then
      echo "FAIL: $name: output does not contain '$needle'" >&2
      cat "$log" >&2
      exit 1
    fi
  fi
  echo "ok: $name"
}

# clean: both directions linked, scope heading present.
d="$(new_fixture clean)"
write_old_linked "$d"
write_new_linked "$d"
commit_all "$d" base
expect clean pass ""

# clean-list-form: a list-valued supersedes links to several ADRs.
d="$(new_fixture clean-list-form)"
write_old_linked "$d"
write_new_linked "$d"
other=2026-01-15-other-decision
printf -- '---\nsuperseded_by: %s\n---\n\n# Other\n' "$new" >"$d/docs/adr/$other.md"
printf -- '---\nsupersedes:\n  - %s\n  - %s\n---\n\n# New decision\n\n## Context\n\n### Supersession scope\n\nBoth.\n\n## Decision\n' "$old" "$other" >"$d/docs/adr/$new.md"
commit_all "$d" base
expect clean-list-form pass ""

# pre-rule-superseding-adr: a superseding ADR dated before the scope rule
# needs no heading, and its list-form supersedes still links both ways.
d="$(new_fixture pre-rule-superseding-adr)"
printf -- '---\nsuperseded_by: 2026-11-03-early\n---\n\n# A\n' >"$d/docs/adr/2026-01-01-a.md"
printf -- '---\nsuperseded_by: 2026-11-03-early\n---\n\n# B\n' >"$d/docs/adr/2026-01-02-b.md"
printf -- '---\nsupersedes:\n  - 2026-01-01-a\n  - 2026-01-02-b\n---\n\n# Early\n' >"$d/docs/adr/2026-11-03-early.md"
commit_all "$d" base
expect pre-rule-superseding-adr pass ""

# list-form-one-directional: every list item must link back.
d="$(new_fixture list-form-one-directional)"
printf -- '---\nsuperseded_by: 2026-11-03-early\n---\n\n# A\n' >"$d/docs/adr/2026-01-01-a.md"
printf -- '# B\n' >"$d/docs/adr/2026-01-02-b.md"
printf -- '---\nsupersedes:\n  - 2026-01-01-a\n  - 2026-01-02-b\n---\n\n# Early\n' >"$d/docs/adr/2026-11-03-early.md"
commit_all "$d" base
expect list-form-one-directional fail "2026-01-02-b.md lacks"

# dangling-supersedes: supersedes names an ADR that does not exist.
d="$(new_fixture dangling-supersedes)"
write_new_linked "$d"
commit_all "$d" base
expect dangling-supersedes fail "does not exist"

# dangling-superseded-by: superseded_by names an ADR that does not exist.
d="$(new_fixture dangling-superseded-by)"
write_old_linked "$d"
commit_all "$d" base
expect dangling-superseded-by fail "does not exist"

# one-directional-supersedes: the old ADR lacks superseded_by.
d="$(new_fixture one-directional-supersedes)"
write_old_plain "$d"
write_new_linked "$d"
commit_all "$d" base
expect one-directional-supersedes fail "is one-directional"

# one-directional-superseded-by: the new ADR lacks supersedes.
d="$(new_fixture one-directional-superseded-by)"
write_old_linked "$d"
printf '# New decision\n' >"$d/docs/adr/$new.md"
commit_all "$d" base
expect one-directional-superseded-by fail "is one-directional"

# missing-scope-heading: a heading inside a code fence does not count.
d="$(new_fixture missing-scope-heading)"
write_old_linked "$d"
printf -- '---\nsupersedes: %s\n---\n\n# New decision\n\n```\n## Supersession scope\n```\n' "$old" >"$d/docs/adr/$new.md"
commit_all "$d" base
expect missing-scope-heading fail "Supersession scope"

# body-edit: a body change to an accepted ADR fails and names the line.
d="$(new_fixture body-edit)"
write_old_plain "$d"
commit_all "$d" base
printf '\nAn added scope sentence.\n' >>"$d/docs/adr/$old.md"
commit_all "$d" "edit body"
expect body-edit fail "An added scope sentence." HEAD~1 HEAD

# body-edit-with-link: adding superseded_by does not excuse a body change.
d="$(new_fixture body-edit-with-link)"
write_old_plain "$d"
write_new_linked "$d"
commit_all "$d" base
write_old_linked "$d"
printf '\nAn added scope sentence.\n' >>"$d/docs/adr/$old.md"
commit_all "$d" "link and edit body"
expect body-edit-with-link fail "An added scope sentence." HEAD~1 HEAD

# misplaced-scope-heading: the heading must come between Context and Decision.
d="$(new_fixture misplaced-scope-heading)"
write_old_linked "$d"
printf -- '---\nsupersedes: %s\n---\n\n# New decision\n\n## Decision\n\n## Context\n\n## Supersession scope\n' "$old" >"$d/docs/adr/$new.md"
commit_all "$d" base
expect misplaced-scope-heading fail "between its"

# frontmatter-only-without-fence: the file had no frontmatter, so the fence
# and the superseded_by line are added together.
d="$(new_fixture frontmatter-only-without-fence)"
write_old_plain "$d"
write_new_linked "$d"
commit_all "$d" base
write_old_linked "$d"
commit_all "$d" "supersede"
expect frontmatter-only-without-fence pass "" HEAD~1 HEAD

# frontmatter-only-with-fence: the file already had frontmatter (its own
# supersedes), and gains superseded_by inside it.
d="$(new_fixture frontmatter-only-with-fence)"
printf -- '---\nsuperseded_by: mid\n---\n\n# Old\n' >"$d/docs/adr/$old.md"
mid_body='\n# Mid\n\n## Context\n\n## Supersession scope\n\nAll of it.\n\n## Decision\n'
printf -- "---\nsupersedes: $old\n---\n$mid_body" >"$d/docs/adr/mid.md"
printf -- "---\nsupersedes: mid\n---\n\n# New\n\n## Context\n\n## Supersession scope\n\nAll of it.\n\n## Decision\n" >"$d/docs/adr/$new.md"
commit_all "$d" base
printf -- "---\nsupersedes: $old\nsuperseded_by: $new\n---\n$mid_body" >"$d/docs/adr/mid.md"
commit_all "$d" "supersede mid"
expect frontmatter-only-with-fence pass "" HEAD~1 HEAD

# changed-superseded-by: re-pointing an existing superseded_by is not the
# permitted addition.
d="$(new_fixture changed-superseded-by)"
write_old_linked "$d"
write_new_linked "$d"
commit_all "$d" base
sed -i "s/^superseded_by: .*/superseded_by: somewhere-else/" "$d/docs/adr/$old.md"
commit_all "$d" "repoint"
expect changed-superseded-by fail "accepted ADR changed beyond" HEAD~1 HEAD

# new-file: a newly added ADR is not subject to immutability.
d="$(new_fixture new-file)"
write_old_plain "$d"
commit_all "$d" base
printf '# Brand new\n\nBody.\n' >"$d/docs/adr/$new.md"
commit_all "$d" "add adr"
expect new-file pass "" HEAD~1 HEAD

# no-adr-dir: a tree without docs/adr passes.
d="$work/no-adr-dir"
mkdir -p "$d"
expect no-adr-dir pass ""
