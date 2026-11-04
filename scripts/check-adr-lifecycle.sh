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
      closing = (fchar == "`") ? ($0 ~ /^ {0,3}`+[ \t\r]*$/) : ($0 ~ /^ {0,3}~+[ \t\r]*$/)
      t = $0; gsub(/[ \t\r]/, "", t)
      if (closing && length(t) >= flen) { fenced = 0 }
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

# The one permitted edit is adding entries to `superseded_by`, whether it is a
# single value or a list. Stripping that key from both versions lets a
# byte-for-byte comparison isolate every other change; a frontmatter block left
# empty is dropped with its trailing blank line, so a file that gained its first
# frontmatter compares equal to one that had none.
strip_superseded_by() {
  awk '
    NR == 1 && $0 == "---" { infm = 1; next }
    infm && $0 == "---" {
      infm = 0
      if (kept > 0) {
        print "---"
        for (i = 1; i <= kept; i++) print held[i]
        print "---"
      } else {
        skipblank = 1
      }
      next
    }
    infm {
      if ($0 ~ /^superseded_by:/) { skipping = 1; next }
      if (skipping && $0 ~ /^[ \t]*-[ \t]*/) next
      skipping = 0
      held[++kept] = $0
      next
    }
    skipblank { skipblank = 0; if ($0 == "") next }
    { print }
    END {
      if (infm) {
        print "---"
        for (i = 1; i <= kept; i++) print held[i]
      }
    }
  ' "$1"
}

# Existing entries may not be dropped, so a link cannot be quietly retracted.
superseded_by_entries() {
  local k v
  while read -r k v; do
    if [ "$k" = "superseded_by" ] && [ -n "$v" ]; then
      echo "$v"
    fi
  done < <(frontmatter_links "$1")
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
        report "$path: accepted ADR was removed or changed file type (status $status); only adding \`superseded_by:\` entries is permitted"
        continue
        ;;
    esac
    git show "$merge_base:$path" >"$tmp/base"
    git show "$head_ref:$path" >"$tmp/head"
    strip_superseded_by "$tmp/base" >"$tmp/base.stripped"
    strip_superseded_by "$tmp/head" >"$tmp/head.stripped"
    if ! cmp -s "$tmp/base.stripped" "$tmp/head.stripped"; then
      local diffs diffline
      diffs="$(diff "$tmp/base.stripped" "$tmp/head.stripped" || true)"
      diffline="$(grep -m1 '^[<>][[:space:]]*[^[:space:]]' <<<"$diffs" || grep -m1 '^[<>]' <<<"$diffs" || true)"
      report "$path: accepted ADR changed beyond adding \`superseded_by:\` entries; first differing line: $diffline"
      continue
    fi
    local entry
    while read -r entry; do
      [ -z "$entry" ] && continue
      if ! has_link "$tmp/head" superseded_by "$entry"; then
        report "$path: accepted ADR changed beyond adding \`superseded_by:\` entries; entry \`$entry\` was removed"
      fi
    done < <(superseded_by_entries "$tmp/base")
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
