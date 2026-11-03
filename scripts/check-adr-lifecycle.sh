#!/usr/bin/env bash
# Enforces the ADR lifecycle rules CLAUDE.md states for docs/adr/. The
# immutability rule needs a diff, so it runs only when a base and head ref are
# given; the link and scope rules need only the tree.
#
# The scope heading is a weak proxy for "states which decisions are
# replaced": it catches omission, not vagueness.
set -euo pipefail

root="${ADR_LIFECYCLE_CHECK_ROOT:-$(pwd)}"
cd "$root"

if [ "$#" -ne 0 ] && [ "$#" -ne 2 ]; then
  echo "usage: check-adr-lifecycle.sh [<base-ref> <head-ref>]" >&2
  exit 2
fi

adr_dir="docs/adr"
fail=0

report() {
  echo "check-adr-lifecycle: $1" >&2
  fail=1
}

# A key with no value prints an empty value rather than nothing, so an empty
# `supersedes:` is reported instead of silently reading as no supersession.
frontmatter_links() {
  awk '
    function clean(v) {
      sub(/^[ \t]+/, "", v); sub(/[ \t\r]+$/, "", v)
      gsub(/^["\047]|["\047]$/, "", v)
      return v
    }
    NR == 1 { if ($0 != "---") exit; infm = 1; next }
    infm && $0 == "---" { if (key != "" && !emitted) print key " "; exit }
    !infm { next }
    /^(supersedes|superseded_by):/ {
      if (key != "" && !emitted) print key " "
      key = $0; sub(/:.*/, "", key)
      v = $0; sub(/^[^:]*:/, "", v); v = clean(v)
      emitted = 0
      if (v != "") { print key " " v; emitted = 1 }
      next
    }
    key != "" && /^[ \t]*-[ \t]*/ {
      v = $0; sub(/^[ \t]*-[ \t]*/, "", v); v = clean(v)
      if (v != "") { print key " " v; emitted = 1 }
      next
    }
    { if (key != "" && !emitted) print key " "; key = "" }
  ' "$1"
}

has_link() {
  local file="$1" key="$2" want="$3" k v
  while read -r k v; do
    if [ "$k" = "$key" ] && [ "$v" = "$want" ]; then
      return 0
    fi
  done < <(frontmatter_links "$file")
  return 1
}

# Ordering is checked, not just presence, because CLAUDE.md fixes the section
# between Context and Decision.
has_scope_heading() {
  awk '
    fenced {
      if (match($0, /^ {0,3}[`~]+[ \t\r]*$/)) {
        t = $0; gsub(/[ \t\r]/, "", t)
        if (substr(t, 1, 1) == fchar && length(t) >= flen) { fenced = 0; next }
      }
      next
    }
    /^ {0,3}(```|~~~)/ {
      t = $0; sub(/^ {0,3}/, "", t)
      fchar = substr(t, 1, 1)
      flen = 0
      while (substr(t, flen + 1, 1) == fchar) flen++
      fenced = 1
      next
    }
    /^#{1,6}[ \t]+Context[ \t\r]*$/ && !ctx { ctx = NR }
    /^#{1,6}[ \t]+Decision[ \t\r]*$/ && !dec { dec = NR }
    /^#{1,6}[ \t]+Supersession scope[ \t\r]*$/ && !scope { scope = NR }
    END { exit (ctx && dec && scope && ctx < scope && scope < dec) ? 0 : 1 }
  ' "$1"
}

check_links_and_scope() {
  local f slug key target other want
  for f in "$adr_dir"/*.md; do
    [ -e "$f" ] || continue
    slug="$(basename "$f" .md)"
    local has_supersedes=0
    while read -r key target; do
      [ -z "$key" ] && continue
      if [ -z "$target" ]; then
        report "$f: \`$key:\` has no value"
        continue
      fi
      if [ "$key" = "supersedes" ]; then
        has_supersedes=1
        other="supersedes"
        want="superseded_by"
      else
        other="superseded_by"
        want="supersedes"
      fi
      if [ ! -f "$adr_dir/$target.md" ]; then
        report "$f: $other target $adr_dir/$target.md does not exist"
      elif ! has_link "$adr_dir/$target.md" "$want" "$slug"; then
        report "$f: \`$key: $target\` is one-directional; $adr_dir/$target.md lacks \`$want: $slug\`"
      fi
    done < <(frontmatter_links "$f")
    if [ "$has_supersedes" -eq 1 ] && ! has_scope_heading "$f"; then
      report "$f: declares \`supersedes:\` but has no \`Supersession scope\` heading between its \`Context\` and \`Decision\` headings"
    fi
  done
}

# Undoing the one permitted edit lets a byte-for-byte comparison with the base
# version isolate every other change, including a rewritten superseded_by.
undo_permitted_edit() {
  local base_file="$1" head_file="$2"
  if [ "$(head -n 1 "$base_file")" = "---" ]; then
    awk '
      NR == 1 { infm = 1; print; next }
      infm && $0 == "---" { infm = 0; print; next }
      infm && !dropped && /^superseded_by:[ \t]*[^ \t]/ { dropped = 1; next }
      { print }
    ' "$head_file"
    return
  fi
  awk '
    NR == 1 { if ($0 == "---") { infm = 1; next } }
    infm && NR == 2 && /^superseded_by:[ \t]*[^ \t]/ { next }
    infm && NR == 3 && $0 == "---" { infm = 0; skipblank = 1; next }
    skipblank { skipblank = 0; if ($0 == "") next }
    { print }
  ' "$head_file"
}

check_immutability() {
  local base_ref="$1" head_ref="$2" merge_base status path tmp
  if ! merge_base="$(git merge-base "$base_ref" "$head_ref")"; then
    report "no merge base between $base_ref and $head_ref"
    return
  fi
  tmp="$(mktemp -d)"
  while IFS=$'\t' read -r status path; do
    case "$status" in
      A) continue ;;
      M) ;;
      *)
        report "$path: accepted ADR was removed or changed file type (status $status); only a frontmatter \`superseded_by:\` addition is permitted"
        continue
        ;;
    esac
    git show "$merge_base:$path" >"$tmp/base"
    git show "$head_ref:$path" >"$tmp/head"
    if [ "$(head -n 1 "$tmp/base")" = "---" ] &&
      awk 'NR > 1 && $0 == "---" { exit } NR > 1 && /^superseded_by:/ { found = 1 } END { exit found ? 0 : 1 }' "$tmp/base"; then
      cp "$tmp/head" "$tmp/undone"
    else
      undo_permitted_edit "$tmp/base" "$tmp/head" >"$tmp/undone"
    fi
    if ! cmp -s "$tmp/base" "$tmp/undone"; then
      local diffs diffline
      diffs="$(diff "$tmp/base" "$tmp/undone" || true)"
      diffline="$(grep -m1 '^[<>][[:space:]]*[^[:space:]]' <<<"$diffs" || grep -m1 '^[<>]' <<<"$diffs" || true)"
      report "$path: accepted ADR changed beyond adding a frontmatter \`superseded_by:\` line; first differing line: $diffline"
    fi
  done < <(git diff --no-renames --name-status "$merge_base" "$head_ref" -- "$adr_dir/*.md")
  rm -rf "$tmp"
}

check_links_and_scope
if [ "$#" -eq 2 ]; then
  check_immutability "$1" "$2"
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "adr-lifecycle check passed"
