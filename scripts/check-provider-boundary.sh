#!/usr/bin/env bash
# Fails if app/ (excluding tests) or contracts/ reference a specific external
# provider by name or assume its identifier shape. Core owns the durable
# structure of work; a provider commitment belongs in plugins/ instead.
#
# A line that genuinely needs to keep a provider token can allowlist itself
# with a trailing "// boundary-allow: <reason>" comment.
set -euo pipefail

root="${BOUNDARY_CHECK_ROOT:-$(pwd)}"
cd "$root"

pattern='(^|[^A-Za-z0-9_.])[Gg]it[Hh]ub([^A-Za-z0-9_.]|$)|(^|[^A-Za-z0-9_])gh([^A-Za-z0-9_]|$)|pvti|[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*-[0-9]+'

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
