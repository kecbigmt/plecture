#!/usr/bin/env bash
# Fails if app/ (excluding tests) or contracts/ reference a specific external
# provider by name or assume its identifier shape. Core owns the durable
# structure of work; a provider commitment belongs in plugins/ instead.
#
# Scope decision: an "owner/repo"-shaped identifier is treated as leakage,
# not as plecture's own vocabulary. Session/resource names are opaque strings
# to core (contracts/event's own doc comment says as much) — an "owner/repo"
# example is always borrowing a specific hosting provider's naming
# convention, so it is caught alongside literal provider names.
#
# A line that genuinely needs to keep a provider token can allowlist itself
# with a trailing "// boundary-allow: <reason>" comment.
set -euo pipefail

root="${BOUNDARY_CHECK_ROOT:-$(pwd)}"
cd "$root"

# Each alternative targets a distinct leak:
#   - a provider name (github/gh/pvti);
#   - a session/resource-id EXAMPLE shaped like a GitHub-style "owner/repo"
#     slug — plecture's own session names are opaque strings core never
#     interprets (see contracts/event doc comment), so a literal "owner/repo"
#     or "<owner>/<repo>" placeholder in core is always a leaked assumption
#     about a specific host's naming convention, never core's own vocabulary.
# This intentionally does NOT flag every "word/word" or "word/word-N"
# string (e.g. Go import paths, generic path components) — only the
# owner/repo placeholder shape itself and identifiers that look like a
# resolved instance of it (an alnum segment, "/", an alnum segment, "-N").
pattern='(^|[^A-Za-z0-9_.])[Gg]it[Hh]ub([^A-Za-z0-9_.]|$)|(^|[^A-Za-z0-9_])gh([^A-Za-z0-9_]|$)|pvti|<?[Oo]wner>?/<?[Rr]epo>?|[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*-[0-9]+'

fail=0
count=0
while IFS= read -r f; do
  [ -z "$f" ] && continue
  count=$((count + 1))
  while IFS=: read -r lineno content; do
    [ -z "$lineno" ] && continue
    case "$content" in
      *boundary-allow:*) continue ;;
      *github.com/*) continue ;; # Go module import path, not a provider reference
    esac
    echo "$f:$lineno: $content"
    fail=1
  done < <(grep -nE "$pattern" "$f" || true)
done < <({ find app -name '*.go' -not -name '*_test.go'; find contracts -name '*.go'; } | sort)

if [ "$fail" -ne 0 ]; then
  echo
  echo "Provider-neutrality boundary violated: core (app/, contracts/) must not" >&2
  echo "name a specific provider or assume its identifier shape. Move the" >&2
  echo "reference to plugins/, or allowlist a genuine exception with a" >&2
  echo "trailing '// boundary-allow: <reason>' comment." >&2
  exit 1
fi

echo "boundary check passed ($count files scanned)"
