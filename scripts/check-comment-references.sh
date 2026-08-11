#!/usr/bin/env bash
# Fails if a Go code comment references an issue, PR, or ADR number. The
# standing rule (CLAUDE.md, AGENTS.md) forbids these because a number tied to
# an external tracker rots the moment the comment is relocated or the
# tracker is renumbered; the comment must stand on its own.
#
# Scope decision: every *.go file is scanned, including *_test.go — the
# standing rule applies to all Go code comments, not just production code.
# Only text inside `//` comments is scanned, not string
# literals (a CLI help string embedding "review#1" as an example, or a URL
# containing a digit, is not a code comment) and not block comments (this
# codebase has none in doc-comment position; `/*` only appears inside glob
# patterns in string literals). A tiny per-line scanner tracks whether each
# character is inside a `"..."` or `` `...` `` literal so a `//` inside a
# string is not mistaken for the start of a comment.
#
# Two patterns are flagged inside a comment:
#   - "ADR-<digits>", the repository's own decision-record numbering.
#   - a bare "#<digits>" not glued to a preceding identifier character, e.g.
#     "#39" or "PR #12". This deliberately excludes task-instance-id examples
#     like "review#1" or "<task>#<n>", where the "#" is glued to a word —
#     those describe sennit's own instance-numbering syntax, not an issue or
#     PR reference.
#
# A line that genuinely needs to keep such a reference can allowlist itself
# with a trailing "// comment-ref-allow: <reason>" comment.
set -euo pipefail

root="${COMMENT_REF_CHECK_ROOT:-$(pwd)}"
cd "$root"

extract_comments() {
  local f="$1"
  awk '
    {
      line = $0
      instr = 0
      n = length(line)
      i = 1
      while (i <= n) {
        c = substr(line, i, 1)
        if (inraw) {
          if (c == "`") inraw = 0
          i++
          continue
        }
        if (instr) {
          if (c == "\\") { i += 2; continue }
          if (c == "\"") instr = 0
          i++
          continue
        }
        if (c == "`") { inraw = 1; i++; continue }
        if (c == "\"") { instr = 1; i++; continue }
        if (c == "/" && substr(line, i + 1, 1) == "/") {
          print FNR ":" substr(line, i + 2)
          next
        }
        i++
      }
    }
  ' "$f"
}

fail=0
count=0
while IFS= read -r f; do
  [ -z "$f" ] && continue
  count=$((count + 1))
  while IFS=: read -r lineno comment; do
    [ -z "$lineno" ] && continue
    case "$comment" in
      *comment-ref-allow:*) continue ;;
    esac
    if echo "$comment" | grep -qiE 'ADR-[0-9]+|(^|[^A-Za-z0-9_])#[0-9]+'; then
      echo "$f:$lineno:$comment"
      fail=1
    fi
  done < <(extract_comments "$f")
done < <(find app contracts plugins -name '*.go' | sort)

if [ "$fail" -ne 0 ]; then
  echo
  echo "Comment references an issue/PR/ADR number. Standing rule: comments must" >&2
  echo "be self-contained, not tied to an external tracker's numbering. Rewrite" >&2
  echo "the comment, or allowlist a genuine exception with a trailing" >&2
  echo "'// comment-ref-allow: <reason>' comment." >&2
  exit 1
fi

echo "comment-reference check passed ($count files scanned)"
